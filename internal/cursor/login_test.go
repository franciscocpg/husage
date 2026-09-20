package cursor

import (
	"testing"
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

func TestLoginRefreshKeepsCachedUsageAndProviderIsolation(t *testing.T) {
	p := New(Options{Home: t.TempDir()})
	p.next = time.Now().Add(time.Hour)
	p.cached = []subscription.Account{{ID: "cursor:current", Windows: []subscription.Window{{Used: 10}}}}
	p.LoginSucceeded(subscription.LoginTarget{Provider: "claude"})
	if p.next.IsZero() {
		t.Fatal("unrelated login bypassed cooldown")
	}
	p.LoginSucceeded(subscription.LoginTarget{Provider: "cursor"})
	if !p.next.IsZero() || len(p.cached[0].Windows) != 1 {
		t.Fatal("login did not invalidate cooldown or lost cached usage")
	}
}
