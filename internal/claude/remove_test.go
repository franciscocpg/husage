package claude

import (
	"errors"
	"github.com/franciscocpg/husage/internal/subscription"
	"path/filepath"
	"testing"
)

func TestRemoveAllProfilesForOneClaudeSubscription(t *testing.T) {
	home := t.TempDir()
	g := NewProfiles(Options{Home: home}, []Options{{Home: home}, {Home: home, ConfigDir: filepath.Join(home, "duplicate")}, {Home: home, ConfigDir: filepath.Join(home, "other")}})
	for i, p := range g.providers {
		id := "same"
		if i == 2 {
			id = "other"
		}
		g.last[p] = subscription.Account{ID: id}
	}
	before := g.providers[2]
	fail := true
	save := func(dirs []string) error {
		if len(dirs) != 2 || dirs[0] != filepath.Join(home, ".claude") {
			t.Fatal(dirs)
		}
		if fail {
			return errors.New("save failed")
		}
		return nil
	}
	if g.Remove("same", save) == nil || len(g.providers) != 3 {
		t.Fatal("failed save removed profiles")
	}
	fail = false
	if err := g.Remove("same", save); err != nil {
		t.Fatal(err)
	}
	if len(g.providers) != 1 || g.providers[0] != before || len(g.last) != 1 {
		t.Fatal("wrong remaining profiles")
	}
	if g.Remove("missing", save) == nil {
		t.Fatal("stale selection accepted")
	}
}
