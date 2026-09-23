// Package claude reads subscription usage and delegates credential renewal to Claude CLI.
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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
	opts             Options
	client           *http.Client
	url              string
	now              func() time.Time
	cached           []subscription.Account
	nextFetch        time.Time
	cachedID         string
	readToken        func(context.Context) (string, error)
	readCredentials  func(context.Context) (oauthCredentials, error)
	renewCredentials func(context.Context, oauthCredentials) error
	authFailed       bool
	authFailedToken  string
	debug            *DebugLog
	sleepGap         func(prev, now time.Time) time.Duration
	lastSeen         time.Time
	awakeSince       time.Time
	rejectedRefresh  *[32]byte
}

func New(opts Options) *Provider {
	return &Provider{opts: opts, url: usageURL, now: time.Now, sleepGap: systemSleep, client: &http.Client{
		Timeout: 15 * time.Second,
		// Credentials must never follow a redirect to another origin.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (p *Provider) Load(ctx context.Context) ([]subscription.Account, error) {
	p.observeClock()
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
		// A user may have completed login since our last failed attempt. Detect
		// that locally so manual reload works without restarting or waiting.
		if !p.authFailed || p.readToken != nil {
			return p.cached
		}
		creds, err := p.credentials(ctx)
		if err != nil || creds.expired(p.now()) || creds.AccessToken == p.authFailedToken {
			return p.cached
		}
	}
	if p.cachedID != id.key() {
		p.cached = nil
	}
	p.cachedID = id.key()
	p.nextFetch = p.now().Add(5 * time.Minute)
	a := subscription.Account{ID: id.key(), Provider: "Claude Code", Name: id.Name, Email: id.Email, Active: true, Source: "Claude API"}
	a.Login = p.loginTarget()
	if a.Name == "" {
		a.Name = "Claude subscription"
	}
	if len(p.cached) > 0 {
		a.Windows = p.cached[0].Windows
		a.UpdatedAt = p.cached[0].UpdatedAt
	}
	if len(a.Windows) == 0 {
		p.restoreUsage(id, &a)
	}
	readToken := p.readToken
	if readToken == nil {
		readToken = p.token
	}
	token, err := readToken(ctx)
	p.authFailed = err != nil
	p.authFailedToken = ""
	if err == nil {
		var windows []subscription.Window
		windows, err = p.fetchForIdentity(ctx, id, token)
		if errors.Is(err, errUsageUnauthorized) && p.readToken == nil {
			p.debugf(nil, "usage request returned HTTP 401; renewing once")
			p.authFailed = true
			p.authFailedToken = token
			token, err = p.renewToken(ctx, token)
			if err == nil {
				windows, err = p.fetchForIdentity(ctx, id, token)
				p.authFailed = errors.Is(err, errUsageUnauthorized)
				p.authFailedToken = token
			}
		}
		if err == nil {
			a.Windows = windows
			a.UpdatedAt = p.now()
			if p.saveUsage(id, a) != nil {
				a.Error = "Usage loaded, but the local cache could not be saved."
			}
		}
	}
	if err != nil {
		p.debugf(nil, "usage load failed: %v", err)
		if errors.Is(err, errSettling) {
			p.nextFetch = p.awakeSince.Add(settleAfterWake)
		}
		a.Error = err.Error()
		a.Warning = recoveryWarning(err)
		a.LoginRequired = requiresLogin(err)
		a.Stale = len(a.Windows) > 0
	}
	p.cached = []subscription.Account{a}
	return p.cached
}

func (p *Provider) fetchForIdentity(ctx context.Context, id identity, token string) ([]subscription.Window, error) {
	if p.readToken == nil {
		current, err := readIdentity(p.opts.identityPath())
		if err != nil || current.key() != id.key() {
			return nil, errors.New("Claude account changed during renewal. Reload to read the new account.")
		}
	}
	return p.fetch(ctx, token)
}

var errUsageUnauthorized = errors.New("Claude usage rejected this login. Sign in again for this profile.")

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
	case http.StatusUnauthorized:
		return nil, errUsageUnauthorized
	case http.StatusForbidden:
		return nil, p.loginRecoveryError("Claude usage access denied.", "Usage access denied. Check this profile's account permissions.")
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
