package codex

import (
	"context"
	"testing"
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

func TestLoginRefreshBypassesCooldownWithoutFileCredentialChanges(t *testing.T) {
	g := NewProfiles([]Options{{Home: t.TempDir()}, {Home: t.TempDir()}})
	p, other := g.providers[0], g.providers[1]
	p.next, other.next = time.Now().Add(time.Hour), time.Now().Add(time.Hour)
	p.cached = []subscription.Account{{ID: "previous", Source: "Codex app-server"}}
	calls := 0
	p.read = func(context.Context, string) (reading, error) { calls++; return reading{}, nil }
	g.LoginSucceeded(subscription.LoginTarget{Provider: "codex", Directory: p.opts.Home})
	accounts, err := p.Load(context.Background())
	if err != nil || calls != 1 || other.next.IsZero() {
		t.Fatal("login refresh missed keychain-only login or invalidated other home")
	}
	if accounts[0].Login == nil || accounts[0].Login.Directory != p.opts.Home {
		t.Fatal("incorrect login target")
	}
	if !accounts[0].LoginRequired {
		t.Fatal("missing account did not request login")
	}
}
