package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/profile"
	"github.com/franciscocpg/husage/internal/subscription"
)

type fakeLogin struct{}

func (fakeLogin) Run() error          { return nil }
func (fakeLogin) SetStdin(io.Reader)  {}
func (fakeLogin) SetStdout(io.Writer) {}
func (fakeLogin) SetStderr(io.Writer) {}

func TestLoginSelectionConfirmationRetryAndRefresh(t *testing.T) {
	accounts, _ := (subscription.Demo{}).Load(context.Background())
	for i := range accounts {
		accounts[i].Login = &subscription.LoginTarget{Provider: "claude", Directory: "/profile/" + accounts[i].ID}
	}
	accounts[1].LoginRequired, accounts[1].Stale = true, true
	calls, refreshed := 0, 0
	selected := *accounts[1].Login
	m := New(context.Background(), subscription.Demo{}, time.Minute, time.UTC, false).WithLoginActions(&LoginActions{
		Command: profile.ExistingLoginCommand,
		Login: func(_ context.Context, target subscription.LoginTarget) tea.ExecCommand {
			calls++
			if target != selected {
				t.Fatal("login targeted the wrong native profile")
			}
			return fakeLogin{}
		},
		Succeeded: func(target subscription.LoginTarget) {
			if target != selected {
				t.Fatal("invalidated wrong profile")
			}
			refreshed++
		},
	})
	m.accounts, m.loaded, m.loading = accounts, true, false
	key := func(code rune) tea.Cmd {
		next, cmd := m.Update(tea.KeyPressMsg{Code: code})
		m = next.(Model)
		return cmd
	}
	if !strings.Contains(strings.Join(m.bodyLines(), "\n"), "Press l to sign in again") {
		t.Fatal("missing login shortcut hint")
	}
	key('l')
	if m.loginIndex != 1 {
		t.Fatal("did not preselect login-required profile")
	}
	key(tea.KeyUp)
	key(tea.KeyDown)
	if cmd := key(tea.KeyEnter); cmd != nil || calls != 0 || !m.loginConfirm {
		t.Fatal("ran before confirmation")
	}
	if !strings.Contains(ansi.Strip(strings.Join(m.loginLines(), "\n")), selected.Directory) {
		t.Fatal("missing exact profile command")
	}
	key(tea.KeyEscape)
	if m.loginOpen || calls != 0 {
		t.Fatal("cancel ran login")
	}
	key('l')
	key(tea.KeyEnter)
	// Auto-reloads pause while selecting/confirming login.
	next, _ := m.Update(tickMsg{at: time.Now(), generation: m.tickGeneration})
	m = next.(Model)
	if m.loading {
		t.Fatal("refresh started during login flow")
	}
	// A refresh already running must complete before native login can run.
	m.loading = true
	if key(tea.KeyEnter) != nil || calls != 0 {
		t.Fatal("login overlapped in-flight refresh")
	}
	next, _ = m.Update(resultMsg{accounts: []subscription.Account{accounts[1], accounts[0]}})
	m = next.(Model)
	if cmd := key(tea.KeyEnter); cmd == nil || calls != 1 || !m.loggingIn {
		t.Fatal("login did not start after confirmation")
	}
	if key(tea.KeyEnter) != nil || calls != 1 {
		t.Fatal("duplicate login")
	}
	next, _ = m.Update(loginFinishedMsg{err: errors.New("Login cancelled; retry.")})
	m = next.(Model)
	if !m.loginOpen || m.loggingIn || m.loginError == "" || refreshed != 0 || len(m.accounts[0].Windows) == 0 {
		t.Fatal("failed login lost cached usage or refreshed")
	}
	if key(tea.KeyEnter) == nil || calls != 2 {
		t.Fatal("retry unavailable")
	}
	next, cmd := m.Update(loginFinishedMsg{})
	m = next.(Model)
	if m.loginOpen || !m.loading || cmd == nil {
		t.Fatal("successful login did not return and refresh")
	}
	msg := cmd()
	if refreshed != 1 {
		t.Fatal("successful login did not invalidate cooldown")
	}
	next, _ = m.Update(msg)
	m = next.(Model)
	if m.loading || len(m.accounts) != 2 {
		t.Fatal("refresh failed or created duplicate profile")
	}
}

func TestLoginScreenBoundsAndUnsupportedProfiles(t *testing.T) {
	actions := &LoginActions{Command: profile.ExistingLoginCommand, Login: func(context.Context, subscription.LoginTarget) tea.ExecCommand {
		t.Fatal("unexpected login")
		return nil
	}}
	m := New(context.Background(), subscription.Demo{}, time.Minute, time.UTC, true).WithLoginActions(actions)
	next, _ := m.Update(tea.KeyPressMsg{Code: 'l'})
	if next.(Model).loginOpen {
		t.Fatal("demo offered login")
	}
	m.demo, m.loaded, m.loading = false, true, false
	m.accounts = []subscription.Account{{Name: "unsupported"}, {Name: "work", Provider: "Claude Code", Login: &subscription.LoginTarget{Provider: "claude", Directory: "/profile/" + strings.Repeat("long", 30)}}}
	next, _ = m.Update(tea.KeyPressMsg{Code: 'l'})
	m = next.(Model)
	if len(m.loginChoices) != 1 {
		t.Fatal("unsupported account offered login")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(Model)
	for _, width := range []int{20, 40, 80, 120} {
		next, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		m = next.(Model)
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		m = next.(Model)
		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatal("login screen overflow")
			}
		}
	}
}
