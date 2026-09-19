package claude

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/franciscocpg/husage/internal/subscription"
)

// Profiles combines independent native Claude logins. Each Provider owns its
// credential source, last successful usage, and rate-limit cooldown.
type Profiles struct {
	mu        sync.Mutex
	active    Options
	providers []*Provider
	last      map[*Provider]subscription.Account
}

// Add registers an already-created configuration while keeping other accounts'
// caches and cooldowns. It serializes with an in-flight usage refresh.
func (g *Profiles) Add(opts Options) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.providers {
		if p.opts == opts {
			return
		}
	}
	g.providers = append(g.providers, New(opts))
}

func NewProfiles(active Options, options []Options) *Profiles {
	group := &Profiles{active: active, last: make(map[*Provider]subscription.Account)}
	seen := map[Options]bool{}
	for _, opts := range options {
		if !seen[opts] {
			group.providers = append(group.providers, New(opts))
			seen[opts] = true
		}
	}
	return group
}

func (o Options) identityPath() string {
	if o.ConfigDir != "" {
		return filepath.Join(o.ConfigDir, ".claude.json")
	}
	return filepath.Join(o.Home, ".claude.json")
}

func (g *Profiles) Load(ctx context.Context) ([]subscription.Account, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	active, _ := readIdentity(g.active.identityPath())
	accounts := []subscription.Account{}
	seen := map[string]int{}
	for _, p := range g.providers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, err := p.Load(ctx)
		if err != nil || len(items) == 0 {
			// Preserve the original empty state for an unconfigured single login.
			if len(g.providers) == 1 && err == nil {
				return accounts, nil
			}
			a, ok := g.last[p]
			if !ok || err == nil {
				name := "Current Claude profile"
				if p.opts.ConfigDir != "" {
					name = filepath.Base(p.opts.ConfigDir)
				}
				a = subscription.Account{ID: "profile:" + p.opts.identityPath(), Provider: "Claude Code", Name: name, Source: "Claude API"}
			}
			if err != nil {
				a.Error = err.Error()
			} else {
				// A logged-out profile must not retain the previous account's usage.
				delete(g.last, p)
				a.Error = "No Claude login found for this profile."
			}
			if p.opts.ConfigDir != "" {
				a.Error += fmt.Sprintf(" Configuration: %s", p.opts.ConfigDir)
			}
			items = []subscription.Account{a}
		} else {
			g.last[p] = items[0]
		}
		for _, a := range items {
			a.Active = active.valid() && a.ID == active.key()
			if index, ok := seen[a.ID]; ok {
				// Duplicate configurations of one subscription produce one card.
				old := accounts[index]
				if (old.Error != "" && a.Error == "") || (old.Error == a.Error && a.UpdatedAt.After(old.UpdatedAt)) {
					accounts[index] = a
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
