package tui

import (
	"context"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/profile"
)

// ProfileActions is optional: demo and read-only views never receive it.
type ProfileActions struct {
	Directory func(string) string
	Login     func(context.Context, string) tea.ExecCommand
	Register  func(context.Context, string) (string, error)
}

type profileLoginFinishedMsg struct{ err error }

type profileAddedMsg struct {
	directory string
	err       error
}

func (m Model) WithProfileActions(actions *ProfileActions) Model {
	m.profileActions = actions
	return m
}

func (m Model) updateProfileKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.savingProfile {
		return m, nil
	}
	if key == "esc" || (m.createdDirectory != "" && key == "enter") {
		m.profileOpen = false
		m.createdDirectory = ""
		return m, nil
	}
	switch key {
	case "pgdown":
		m.profileScroll = min(max(0, len(m.profileLines())-m.bodyHeight()), m.profileScroll+m.bodyHeight())
		return m, nil
	case "pgup":
		m.profileScroll = max(0, min(m.profileScroll, max(0, len(m.profileLines())-m.bodyHeight()))-m.bodyHeight())
		return m, nil
	}
	if m.createdDirectory != "" {
		return m, nil
	}
	if m.confirmProfile {
		if key != "enter" {
			return m, nil
		}
		m.savingProfile = true
		m.profileError = ""
		m.profileScroll = 0
		if m.loginSucceeded {
			return m, m.registerProfile()
		}
		if m.profileLogin == nil {
			m.profileLogin = m.profileActions.Login(m.ctx, string(m.profileName))
		}
		return m, tea.Exec(m.profileLogin, func(err error) tea.Msg { return profileLoginFinishedMsg{err: err} })
	}
	switch key {
	case "enter":
		name := string(m.profileName)
		if err := profile.ValidateName(name); err != nil {
			m.profileError = err.Error()
			m.profileScroll = 1 << 20
			return m, nil
		}
		m.confirmProfile = true
		m.profileError = ""
		m.profileScroll = 0
		return m, nil
	case "backspace":
		if m.profileCursor > 0 {
			m.profileName = append(m.profileName[:m.profileCursor-1], m.profileName[m.profileCursor:]...)
			m.profileCursor--
		}
	case "delete":
		if m.profileCursor < len(m.profileName) {
			m.profileName = append(m.profileName[:m.profileCursor], m.profileName[m.profileCursor+1:]...)
		}
	case "left":
		m.profileCursor = max(0, m.profileCursor-1)
	case "right":
		m.profileCursor = min(len(m.profileName), m.profileCursor+1)
	case "home", "ctrl+a":
		m.profileCursor = 0
	case "end", "ctrl+e":
		m.profileCursor = len(m.profileName)
	case "ctrl+u":
		m.profileName = nil
		m.profileCursor = 0
	default:
		m.insertProfileText(msg.Text)
	}
	m.profileScroll = 0
	return m, nil
}

func (m Model) registerProfile() tea.Cmd {
	return func() tea.Msg {
		dir, err := m.profileActions.Register(m.ctx, string(m.profileName))
		return profileAddedMsg{dir, err}
	}
}

func (m *Model) insertProfileText(text string) {
	for _, r := range text {
		if unicode.IsControl(r) {
			continue
		}
		if len(m.profileName) >= 48 {
			m.profileError = "Profile names are limited to 48 characters."
			break
		}
		next := append([]rune{}, m.profileName[:m.profileCursor]...)
		next = append(next, r)
		next = append(next, m.profileName[m.profileCursor:]...)
		m.profileName = next
		m.profileCursor++
	}
}

func (m Model) profileLines() []string {
	w := m.contentWidth()
	var parts []string
	if m.createdDirectory != "" {
		parts = []string{base.Foreground(green).Bold(true).Render("✓ Profile added"), "", base.Render("Claude login succeeded."), "", dim.Render("Saved to ~/.config/husage/profiles.json"), "", base.Render(safe(m.createdDirectory)), "", dim.Render("Your dashboard is refreshing with the new profile.")}
	} else if m.confirmProfile {
		parts = []string{accent.Bold(true).Render("Confirm Claude login"), "", dim.Render("Press Enter to execute this command:"), ""}
		for _, line := range strings.Split(profile.LoginCommand(m.profileActions.Directory(string(m.profileName))), "\n") {
			parts = append(parts, accent.Render(line))
		}
		parts = append(parts, "", dim.Render("The profile is saved only after login succeeds."))
		if m.loginSucceeded {
			parts = append(parts, "", base.Foreground(green).Render("Login succeeded. Press Enter to retry saving."))
		}
		if m.savingProfile {
			parts = append(parts, "", accent.Render("Completing profile setup…"))
		}
	} else {
		name := string(m.profileName)
		before := string(m.profileName[:m.profileCursor])
		after := string(m.profileName[m.profileCursor:])
		input := accent.Render(before) + base.Reverse(true).Render(" ") + accent.Render(after)
		if name == "" {
			input += dim.Render("  e.g. personal or work")
		}
		directory := "~/.config/husage/claude/<name>"
		if profile.ValidateName(name) == nil {
			directory = m.profileActions.Directory(name)
		}
		parts = []string{accent.Bold(true).Render("Add a Claude profile"), "", base.Bold(true).Render("Profile name"), input, "", dim.Render("Letters, numbers, dashes and underscores."), "", dim.Render("Profile directory:"), base.Render(safe(directory)), "", dim.Render("Next: review the login command.")}
	}
	if m.profileError != "" {
		parts = append(parts, "", base.Foreground(amber).Render(safe(m.profileError)))
	}
	var lines []string
	for _, part := range parts {
		lines = append(lines, strings.Split(ansi.Hardwrap(part, w, true), "\n")...)
	}
	return lines
}
