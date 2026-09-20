package codex

import (
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

func (g *Profiles) LoginSucceeded(target subscription.LoginTarget) {
	if target.Provider != "codex" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.providers {
		if p.opts.Home == target.Directory {
			p.next = time.Time{}
		}
	}
}
