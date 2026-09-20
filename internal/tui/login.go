package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/subscription"
)

type LoginActions struct {
	Command   func(subscription.LoginTarget) string
	Login     func(context.Context, subscription.LoginTarget) tea.ExecCommand
	Succeeded func(subscription.LoginTarget)
}

type loginFinishedMsg struct{ err error }

func (m Model) WithLoginActions(actions *LoginActions) Model { m.loginActions = actions; return m }

func (m Model) updateLoginKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.loggingIn {
		return m, nil
	}
	switch key {
	case "esc":
		m.loginOpen = false
	case "pgdown":
		m.loginScroll = min(max(0, len(m.loginLines())-m.bodyHeight()), m.loginScroll+m.bodyHeight())
	case "pgup":
		m.loginScroll = max(0, min(m.loginScroll, max(0, len(m.loginLines())-m.bodyHeight()))-m.bodyHeight())
	case "up", "k":
		if !m.loginConfirm {
			m.loginIndex = max(0, m.loginIndex-1)
			m.loginScroll = 0
		}
	case "down", "j":
		if !m.loginConfirm {
			m.loginIndex = max(0, min(len(m.loginChoices)-1, m.loginIndex+1))
			m.loginScroll = 0
		}
	case "enter":
		if len(m.loginChoices) == 0 {
			return m, nil
		}
		if !m.loginConfirm {
			m.loginConfirm = true
			m.loginScroll = 0
			return m, nil
		}
		// Wait for a refresh that was already in flight when the picker opened.
		if m.loading {
			return m, nil
		}
		m.loggingIn, m.loginError, m.loginScroll = true, "", 0
		target := *m.loginChoices[m.loginIndex].Login
		return m, tea.Exec(m.loginActions.Login(m.ctx, target), func(err error) tea.Msg { return loginFinishedMsg{err: err} })
	}
	return m, nil
}

func (m Model) loginLines() []string {
	parts := []string{accent.Bold(true).Render("Log in again"), ""}
	if len(m.loginChoices) == 0 {
		parts = append(parts, dim.Render("No subscriptions support login here."))
	} else {
		a := m.loginChoices[m.loginIndex]
		if !m.loginConfirm {
			parts = append(parts, dim.Render(fmt.Sprintf("Subscription %d of %d · ↑↓ to select", m.loginIndex+1, len(m.loginChoices))), "")
		}
		parts = append(parts, accent.Bold(true).Render(safe(a.Provider+" · "+a.Name)), dim.Render(safe(a.Email)), "")
		if m.loginConfirm {
			parts = append(parts, dim.Render("Press Enter to execute this command:"), "")
			for _, line := range strings.Split(m.loginActions.Command(*a.Login), "\n") {
				parts = append(parts, accent.Render(safe(line)))
			}
			parts = append(parts, "", dim.Render("This signs in to the existing profile. No new profile is added."))
			if a.Provider == "Cursor" {
				parts = append(parts, dim.Render("Cursor uses its shared native CLI account."))
			}
			if m.loading {
				parts = append(parts, dim.Render("Waiting for the current refresh to finish…"))
			}
		} else {
			parts = append(parts, dim.Render("Press Enter to review the login command."))
		}
	}
	if m.loginError != "" {
		parts = append(parts, "", base.Foreground(amber).Render(safe(m.loginError)))
	}
	var lines []string
	for _, part := range parts {
		lines = append(lines, strings.Split(ansi.Hardwrap(part, m.contentWidth(), true), "\n")...)
	}
	return lines
}
