package claude

import (
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

func (p *Provider) loginTarget() *subscription.LoginTarget {
	return &subscription.LoginTarget{Provider: "claude", Directory: p.opts.ConfigDir, SecureDirectory: p.opts.SecureDir}
}

func (g *Profiles) LoginSucceeded(target subscription.LoginTarget) {
	if target.Provider != "claude" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.providers {
		if *p.loginTarget() == target {
			p.nextFetch = time.Time{}
		}
	}
}
