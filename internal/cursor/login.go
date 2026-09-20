package cursor

import (
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

func (p *Provider) LoginSucceeded(target subscription.LoginTarget) {
	if target.Provider != "cursor" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next = time.Time{}
}
