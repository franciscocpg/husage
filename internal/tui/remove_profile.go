package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/subscription"
)

type RemoveActions struct {
	Path         string
	OverrideHint bool
	Remove       func(context.Context, subscription.Account) error
}
type removedMsg struct {
	account subscription.Account
	err     error
}

func (m Model) WithRemoveActions(actions *RemoveActions) Model { m.removeActions = actions; return m }
func accountKey(a subscription.Account) string                 { return a.Provider + "\x00" + a.ID }
func (m Model) withoutRemoved(accounts []subscription.Account) []subscription.Account {
	kept := make([]subscription.Account, 0, len(accounts))
	for _, a := range accounts {
		if !m.removed[accountKey(a)] {
			kept = append(kept, a)
		}
	}
	return kept
}

func (m Model) updateRemoveKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.removing {
		return m, nil
	}
	if key == "esc" {
		m.removeOpen = false
		return m, nil
	}
	switch key {
	case "pgdown":
		m.removeScroll = min(max(0, len(m.removeLines())-m.bodyHeight()), m.removeScroll+m.bodyHeight())
	case "pgup":
		m.removeScroll = max(0, min(m.removeScroll, max(0, len(m.removeLines())-m.bodyHeight()))-m.bodyHeight())
	case "up", "k":
		if !m.removeConfirm {
			m.removeIndex = max(0, m.removeIndex-1)
			m.removeScroll = 0
		}
	case "down", "j":
		if !m.removeConfirm {
			m.removeIndex = max(0, min(len(m.removeChoices)-1, m.removeIndex+1))
			m.removeScroll = 0
		}
	case "enter":
		if len(m.removeChoices) == 0 {
			return m, nil
		}
		if !m.removeConfirm {
			m.removeConfirm = true
			m.removeScroll = 0
			return m, nil
		}
		m.removing = true
		m.removeError = ""
		a := m.removeChoices[m.removeIndex]
		return m, func() tea.Msg { return removedMsg{a, m.removeActions.Remove(m.ctx, a)} }
	}
	return m, nil
}

func (m Model) removeLines() []string {
	parts := []string{accent.Bold(true).Render("Remove a subscription"), ""}
	if len(m.removeChoices) == 0 {
		parts = append(parts, dim.Render("No subscriptions to remove."))
	} else {
		a := m.removeChoices[m.removeIndex]
		if !m.removeConfirm {
			// Show the selected subscription independently of list size or terminal
			// height; navigation never leaves the selected row off-screen.
			parts = append(parts, dim.Render(fmt.Sprintf("Subscription %d of %d · ↑↓ to select", m.removeIndex+1, len(m.removeChoices))), "")
		}
		parts = append(parts, accent.Bold(true).Render(safe(a.Provider+" · "+a.Name)), dim.Render(safe(a.Email)))
		if a.Active {
			parts = append(parts, dim.Render("Current native login"))
		}
		parts = append(parts, "", base.Render("Remove this subscription from husage?"))
		if a.Provider == "Claude Code" || a.Provider == "Codex" {
			parts = append(parts, base.Foreground(amber).Render("Profiles under ~/.config/husage/claude or codex will be permanently deleted, including their files and credentials."), dim.Render("Default native homes and external profile directories will be kept."))
		} else {
			parts = append(parts, dim.Render("Native login and credentials will be kept."))
		}
		parts = append(parts, "", dim.Render("Profile list: "+safe(m.removeActions.Path)))
		if m.removeActions.OverrideHint {
			parts = append(parts, "", dim.Render("Explicit --claude-dir / --codex-home flags can show it again on a future launch."))
		}
		if m.removeConfirm {
			parts = append(parts, "", base.Foreground(amber).Render("Press Enter to confirm removal. Esc cancels."))
		}
	}
	if m.removeError != "" {
		parts = append(parts, "", base.Foreground(amber).Render(safe(m.removeError)))
	}
	if m.removing {
		parts = append(parts, "", dim.Render("Removing subscription…"))
	}
	var lines []string
	for _, p := range parts {
		lines = append(lines, strings.Split(ansi.Hardwrap(p, m.contentWidth(), true), "\n")...)
	}
	return lines
}
