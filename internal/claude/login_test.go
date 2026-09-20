package claude

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

func TestLoginRefreshUsesExactClaudeStoreAndKeepsOtherCooldowns(t *testing.T) {
	p, creds := credentialProvider(t)
	creds.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
	p.opts.SecureDir = t.TempDir()
	calls := 0
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"five_hour":{"utilization":12}}`), nil
	})
	first, err := p.Load(context.Background())
	if err != nil || len(first) != 1 || first[0].Error != "" {
		t.Fatalf("initial load: %v %v", first, err)
	}
	target := first[0].Login
	if target == nil || target.Directory != p.opts.ConfigDir || target.SecureDirectory != p.opts.SecureDir {
		t.Fatal("lost native login configuration")
	}
	other := New(Options{Home: p.opts.Home, ConfigDir: p.opts.ConfigDir, SecureDir: t.TempDir()})
	other.nextFetch = p.nextFetch
	g := &Profiles{providers: []*Provider{p, other}}
	p.Load(context.Background())
	if calls != 1 {
		t.Fatal("cooldown not respected")
	}
	g.LoginSucceeded(*target)
	if !p.nextFetch.IsZero() || other.nextFetch.IsZero() || len(p.cached[0].Windows) == 0 {
		t.Fatal("wrong store invalidated or cached usage lost")
	}
	p.Load(context.Background())
	if calls != 2 {
		t.Fatal("successful login did not bypass cooldown")
	}
	defaultTarget := New(Options{Home: t.TempDir()}).loginTarget()
	if *defaultTarget != (subscription.LoginTarget{Provider: "claude"}) {
		t.Fatal("default native login was redirected")
	}
}
