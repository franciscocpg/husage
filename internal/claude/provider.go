// Package claude reads Claude subscription usage without changing login state.
package claude

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

const usageURL = "https://api.anthropic.com/api/oauth/usage"

type Options struct {
	Home      string
	ConfigDir string
	SecureDir string
}

type Provider struct {
	opts      Options
	client    *http.Client
	url       string
	now       func() time.Time
	cached    []subscription.Account
	nextFetch time.Time
	cachedID  string
	readToken func(context.Context) (string, error)
}

func New(opts Options) *Provider {
	return &Provider{opts: opts, url: usageURL, now: time.Now, client: &http.Client{
		Timeout: 15 * time.Second,
		// Credentials must never follow a redirect to another origin.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (p *Provider) Load(ctx context.Context) ([]subscription.Account, error) {
	id, err := readIdentity(p.opts.identityPath())
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.New("cannot read Claude account metadata")
	}
	if !id.valid() {
		p.cached = nil
		p.cachedID = ""
		p.nextFetch = time.Time{}
		return []subscription.Account{}, nil
	}
	return p.loadCurrent(ctx, id), nil
}

func (p *Provider) loadCurrent(ctx context.Context, id identity) []subscription.Account {
	// A new login/account must not inherit another account's usage or cooldown.
	if p.cachedID == id.key() && p.now().Before(p.nextFetch) {
		return p.cached
	}
	if p.cachedID != id.key() {
		p.cached = nil
	}
	p.cachedID = id.key()
	p.nextFetch = p.now().Add(5 * time.Minute)
	a := subscription.Account{ID: id.key(), Provider: "Claude Code", Name: id.Name, Email: id.Email, Active: true, Source: "Claude API"}
	if a.Name == "" {
		a.Name = "Claude subscription"
	}
	if len(p.cached) > 0 {
		a.Windows = p.cached[0].Windows
		a.UpdatedAt = p.cached[0].UpdatedAt
	}
	readToken := p.readToken
	if readToken == nil {
		readToken = p.token
	}
	token, err := readToken(ctx)
	if err == nil {
		var windows []subscription.Window
		windows, err = p.fetch(ctx, token)
		if err == nil {
			a.Windows = windows
			a.UpdatedAt = p.now()
		}
	}
	if err != nil {
		a.Error = err.Error()
	}
	p.cached = []subscription.Account{a}
	return p.cached
}

func (p *Provider) token(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dir := p.opts.SecureDir
	if dir == "" {
		dir = p.opts.ConfigDir
	}
	service := "Claude Code-credentials"
	if dir != "" {
		service += fmt.Sprintf("-%x", sha256.Sum256([]byte(dir)))[:9]
	}
	if dir == "" {
		dir = filepath.Join(p.opts.Home, ".claude")
	}
	var data []byte
	if runtime.GOOS == "darwin" {
		// Output is parsed in memory and never printed or persisted.
		data, _ = exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", service, "-w").Output()
	}
	if len(data) == 0 {
		data, _ = os.ReadFile(filepath.Join(dir, ".credentials.json"))
	}
	var creds struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &creds) != nil || creds.OAuth.AccessToken == "" {
		return "", errors.New("No Claude login found. Run claude and use /login, then restart husage.")
	}
	if creds.OAuth.ExpiresAt > 0 && p.now().UnixMilli() >= creds.OAuth.ExpiresAt {
		return "", errors.New("Claude login expired. Open Claude Code to renew it, then restart husage.")
	}
	return creds.OAuth.AccessToken, nil
}

type apiWindow struct {
	Used  *float64   `json:"utilization"`
	Reset *time.Time `json:"resets_at"`
}

func (p *Provider) fetch(ctx context.Context, token string) ([]subscription.Window, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return nil, errors.New("could not prepare usage request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "husage/0.1.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, errors.New("Cannot reach Claude usage. Check your connection; retrying in 5 minutes.")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errors.New("Claude usage access denied. Open Claude Code and use /login.")
	case http.StatusTooManyRequests:
		delay := 5 * time.Minute
		if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n > 0 && n <= 86400 {
			delay = max(delay, time.Duration(n)*time.Second)
		} else if t, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			delay = max(delay, t.Sub(p.now()))
		}
		p.nextFetch = p.now().Add(delay)
		return nil, fmt.Errorf("Claude rate limit; retry after %s.", p.nextFetch.Local().Format("3:04pm"))
	default:
		return nil, fmt.Errorf("Claude usage unavailable (HTTP %d). Retrying in 5 minutes.", resp.StatusCode)
	}
	var raw map[string]json.RawMessage
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw) != nil {
		return nil, errors.New("Claude returned an invalid usage response")
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		if key == "five_hour" || key == "seven_day" || strings.HasPrefix(key, "seven_day_") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	windows := []subscription.Window{}
	for _, key := range keys {
		if string(raw[key]) == "null" {
			continue
		}
		var w apiWindow
		if json.Unmarshal(raw[key], &w) != nil || w.Used == nil || *w.Used < 0 || *w.Used > 100 {
			return nil, errors.New("Claude returned an invalid usage window")
		}
		label := "Current session"
		if key == "seven_day" {
			label = "Current week (all models)"
		} else if strings.HasPrefix(key, "seven_day_") {
			model := strings.ReplaceAll(strings.TrimPrefix(key, "seven_day_"), "_", " ")
			if len(model) > 0 {
				model = strings.ToUpper(model[:1]) + model[1:]
			}
			label = "Current week (" + model + ")"
		}
		v := subscription.Window{Label: label, Used: *w.Used}
		if w.Reset != nil {
			v.ResetsAt = *w.Reset
		}
		windows = append(windows, v)
	}
	// Newer Claude accounts expose model caps in limits instead of seven_day_*.
	if data, ok := raw["limits"]; ok {
		var limits []struct {
			Kind    string     `json:"kind"`
			Percent *float64   `json:"percent"`
			Reset   *time.Time `json:"resets_at"`
			Scope   struct {
				Model struct {
					Name string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		}
		if json.Unmarshal(data, &limits) != nil {
			return nil, errors.New("Claude returned invalid scoped usage limits")
		}
		for _, limit := range limits {
			if limit.Kind != "weekly_scoped" || limit.Scope.Model.Name == "" || limit.Percent == nil {
				continue
			}
			if *limit.Percent < 0 || *limit.Percent > 100 {
				return nil, errors.New("Claude returned an invalid usage percentage")
			}
			w := subscription.Window{Label: "Current week (" + limit.Scope.Model.Name + ")", Used: *limit.Percent}
			if limit.Reset != nil {
				w.ResetsAt = *limit.Reset
			}
			found := false
			for i, old := range windows {
				if strings.EqualFold(old.Label, w.Label) {
					windows[i] = w
					found = true
					break
				}
			}
			if !found {
				windows = append(windows, w)
			}
		}
	}
	if len(windows) == 0 {
		return nil, errors.New("No subscription usage windows returned by Claude")
	}
	return windows, nil
}
