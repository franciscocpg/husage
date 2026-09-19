package codex

import (
	"errors"
	"github.com/franciscocpg/husage/internal/subscription"
	"testing"
)

func TestRemoveAllHomesForOneCodexSubscription(t *testing.T) {
	g := NewProfiles([]Options{{Home: t.TempDir()}, {Home: t.TempDir()}, {Home: t.TempDir()}})
	for i, p := range g.providers {
		id := "same"
		if i == 2 {
			id = "other"
		}
		p.cached = []subscription.Account{{ID: id}}
	}
	before := g.providers[2]
	fail := true
	save := func(dirs []string) error {
		if len(dirs) != 2 {
			t.Fatal(dirs)
		}
		if fail {
			return errors.New("save failed")
		}
		return nil
	}
	if g.Remove("same", save) == nil || len(g.providers) != 3 {
		t.Fatal("failed save removed homes")
	}
	fail = false
	if err := g.Remove("same", save); err != nil {
		t.Fatal(err)
	}
	if len(g.providers) != 1 || g.providers[0] != before {
		t.Fatal("wrong remaining homes")
	}
	if g.Remove("missing", save) == nil {
		t.Fatal("stale selection accepted")
	}
}
