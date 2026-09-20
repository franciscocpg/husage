// Package codex reads native Codex subscription limits through app-server.
package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/franciscocpg/husage/internal/codexcli"
	"github.com/franciscocpg/husage/internal/subscription"
)

type limitWindow struct {
	Used    *float64 `json:"usedPercent"`
	Minutes *int64   `json:"windowDurationMins"`
	Reset   *int64   `json:"resetsAt"`
}
type limitBucket struct {
	ID        string       `json:"limitId"`
	Name      string       `json:"limitName"`
	Primary   *limitWindow `json:"primary"`
	Secondary *limitWindow `json:"secondary"`
}
type rateLimits struct {
	AccountID string                 `json:"accountId"`
	Default   limitBucket            `json:"rateLimits"`
	Buckets   map[string]limitBucket `json:"rateLimitsByLimitId"`
}

func (r rateLimits) windows() ([]subscription.Window, error) {
	buckets := r.Buckets
	if len(buckets) == 0 {
		buckets = map[string]limitBucket{"codex": r.Default}
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var windows []subscription.Window
	for _, key := range keys {
		bucket := buckets[key]
		for i, w := range []*limitWindow{bucket.Primary, bucket.Secondary} {
			if w == nil {
				continue
			}
			if w.Used == nil || *w.Used < 0 || *w.Used > 100 {
				return nil, errors.New("Codex returned invalid usage percentages.")
			}
			label := "Primary limit"
			if i == 1 {
				label = "Secondary limit"
			}
			if w.Minutes != nil && *w.Minutes > 0 {
				switch *w.Minutes {
				case 300:
					label = "Current session (5h)"
				case 10080:
					label = "Current week"
				default:
					if *w.Minutes%60 == 0 {
						label = fmt.Sprintf("%dh limit", *w.Minutes/60)
					} else {
						label = fmt.Sprintf("%dm limit", *w.Minutes)
					}
				}
			}
			if key != "codex" {
				name := bucket.Name
				if name == "" {
					name = key
				}
				label += " (" + name + ")"
			}
			window := subscription.Window{Label: label, Used: *w.Used}
			if w.Reset != nil && *w.Reset > 0 {
				window.ResetsAt = time.Unix(*w.Reset, 0)
			}
			windows = append(windows, window)
		}
	}
	if len(windows) == 0 {
		return nil, errors.New("Codex returned no subscription usage windows for this account.")
	}
	return windows, nil
}

type Options struct {
	Home     string // Exact CODEX_HOME for this account.
	Active   bool
	Optional bool // Auto-discovery omits missing/unsigned-in defaults.
}
type Provider struct {
	opts      Options
	now       func() time.Time
	read      func(context.Context, string) (reading, error)
	next      time.Time
	cached    []subscription.Account
	authStamp string
}

func New(opts Options) *Provider { return &Provider{opts: opts, now: time.Now, read: readUsage} }

func (p *Provider) Load(ctx context.Context) ([]subscription.Account, error) {
	stamp := credentialStamp(p.opts.Home)
	if stamp == p.authStamp && p.now().Before(p.next) {
		return p.cached, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.next = p.now().Add(5 * time.Minute)
	defer func() { p.authStamp = credentialStamp(p.opts.Home) }()
	name := strings.TrimPrefix(filepath.Base(p.opts.Home), ".")
	if name == "codex" {
		name = "Codex"
	}
	a := subscription.Account{ID: "codex:home:" + p.opts.Home, Provider: "Codex", Name: name, Active: p.opts.Active, Source: "Codex app-server"}
	a.Login = &subscription.LoginTarget{Provider: "codex", Directory: p.opts.Home}
	info, err := os.Stat(p.opts.Home)
	if err != nil || !info.IsDir() {
		if p.opts.Optional && os.IsNotExist(err) {
			p.cached = nil
			return nil, nil
		}
		a.Error = "Codex home is missing or is not a directory."
	} else {
		result, err := p.read(ctx, p.opts.Home)
		if result.Account != nil {
			a.Email = result.Account.Email
			if result.Account.Type == "chatgpt" {
				a.Name += " · " + planLabel(result.Account.Plan)
			}
		}
		switch {
		case err != nil:
			a.Error = err.Error()
		case result.Account == nil:
			if p.opts.Optional {
				p.cached = nil
				return nil, nil
			}
			a.Error = "No ChatGPT login found for this Codex home."
			a.LoginRequired = true
		case result.Account.Type != "chatgpt":
			a.Error = "Codex uses non-subscription authentication here. Sign in with ChatGPT to read subscription limits."
			a.LoginRequired = true
		default:
			a.Windows, err = result.Limits.windows()
			if err != nil {
				a.Error = err.Error()
			} else {
				a.UpdatedAt = p.now()
				// Email alone cannot distinguish separate subscriptions/workspaces.
				if result.Limits.AccountID != "" && a.Email != "" {
					a.ID = "codex:" + result.Limits.AccountID + ":" + a.Email
				}
			}
		}
	}
	if a.Error != "" {
		a.Error += " Login: " + strings.ReplaceAll(codexcli.LoginCommand(p.opts.Home), " \\\n", " ")
	}
	p.cached = []subscription.Account{a}
	return p.cached, nil
}

type Profiles struct {
	mu        sync.Mutex
	providers []*Provider
}

// Detect native file-login changes without reading tokens or starting Codex on
// every local redraw. Keychain-only changes are picked up at the next poll.
func credentialStamp(home string) string {
	info, err := os.Stat(filepath.Join(home, "auth.json"))
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
}

func planLabel(plan string) string {
	switch plan {
	case "prolite":
		return "Pro Lite"
	case "self_serve_business_prolite":
		return "Business Pro Lite"
	case "self_serve_business_usage_based":
		return "Business"
	case "ent26", "enterprise_cbp_automation", "enterprise_cbp_usage_based":
		return "Enterprise"
	case "", "unknown":
		return "ChatGPT"
	}
	words := strings.Fields(strings.ReplaceAll(plan, "_", " "))
	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func NewProfiles(options []Options) *Profiles {
	g := &Profiles{}
	for _, o := range options {
		g.Add(o)
	}
	return g
}
func (g *Profiles) Add(o Options) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.providers {
		if p.opts.Home == o.Home {
			return
		}
	}
	g.providers = append(g.providers, New(o))
}
func (g *Profiles) Load(ctx context.Context) ([]subscription.Account, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var accounts []subscription.Account
	seen := map[string]int{}
	for _, p := range g.providers {
		items, err := p.Load(ctx)
		if err != nil {
			return accounts, err
		}
		for _, a := range items {
			if i, ok := seen[a.ID]; ok {
				if a.Active {
					accounts[i].Active = true
				}
				continue
			}
			seen[a.ID] = len(accounts)
			accounts = append(accounts, a)
		}
	}
	sort.SliceStable(accounts, func(i, j int) bool { return accounts[i].Active && !accounts[j].Active })
	return accounts, nil
}
