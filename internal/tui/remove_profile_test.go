package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/subscription"
)

func TestRemoveSelectionConfirmationAndRefreshRace(t *testing.T) {
	accounts, _ := (subscription.Demo{}).Load(context.Background())
	calls := 0
	fail := true
	m := New(context.Background(), subscription.Demo{}, time.Minute, time.UTC, false).WithRemoveActions(&RemoveActions{Path: "/profiles.json", Remove: func(_ context.Context, a subscription.Account) error {
		calls++
		if a.ID != accounts[1].ID {
			t.Fatal("removed wrong subscription")
		}
		if fail {
			return errors.New("save failed")
		}
		return nil
	}})
	m.accounts, m.loaded, m.loading = accounts, true, false
	key := func(code rune) tea.Cmd {
		next, cmd := m.Update(tea.KeyPressMsg{Code: code})
		m = next.(Model)
		return cmd
	}
	key('d')
	key(tea.KeyDown)
	if m.removeIndex != 1 || !strings.Contains(ansi.Strip(m.View().Content), "Personal") {
		t.Fatal("selection inaccessible")
	}
	if cmd := key(tea.KeyEnter); cmd != nil || calls != 0 || !m.removeConfirm {
		t.Fatal("removed without confirmation")
	}
	key(tea.KeyEscape)
	if m.removeOpen || calls != 0 || len(m.accounts) != 2 {
		t.Fatal("cancel removed account")
	}
	key('d')
	key(tea.KeyDown)
	key(tea.KeyEnter)
	// A background update must not change the subscription awaiting confirmation.
	next, _ := m.Update(resultMsg{accounts: []subscription.Account{accounts[1], accounts[0]}})
	m = next.(Model)
	cmd := key(tea.KeyEnter)
	if cmd == nil || !m.removing {
		t.Fatal("confirmation did not schedule removal")
	}
	if key(tea.KeyEnter) != nil {
		t.Fatal("duplicate removal while saving")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if !m.removeOpen || m.removeError != "save failed" || len(m.accounts) != 2 {
		t.Fatal("save failure hid subscription")
	}
	fail = false
	cmd = key(tea.KeyEnter)
	m.loading = true // A pre-removal refresh is still in flight.
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.removeOpen || len(m.accounts) != 1 || m.accounts[0].ID != accounts[0].ID {
		t.Fatal("successful removal did not update dashboard")
	}
	next, reload := m.Update(resultMsg{accounts: accounts})
	m = next.(Model)
	if reload == nil || len(m.accounts) != 1 {
		t.Fatal("old refresh resurrected removed subscription")
	}
}

func TestRemovalViewBoundsAndReadonlyDemo(t *testing.T) {
	accounts, _ := (subscription.Demo{}).Load(context.Background())
	m := New(context.Background(), subscription.Demo{}, time.Minute, time.UTC, true)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'd'})
	if next.(Model).removeOpen {
		t.Fatal("demo offered removal")
	}
	m = m.WithRemoveActions(&RemoveActions{Path: strings.Repeat("/long", 40)})
	m.accounts = accounts
	next, _ = m.Update(tea.KeyPressMsg{Code: 'd'})
	m = next.(Model)
	for _, width := range []int{20, 40, 80, 120} {
		next, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		m = next.(Model)
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m = next.(Model)
		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatal("removal view overflow")
			}
		}
	}
}
