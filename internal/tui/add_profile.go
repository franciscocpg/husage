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
	Create    func(context.Context, string) (string, error)
}

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
	switch key {
	case "enter":
		name := string(m.profileName)
		if err := profile.ValidateName(name); err != nil {
			m.profileError = err.Error()
			m.profileScroll = 1 << 20
			return m, nil
		}
		m.savingProfile = true
		m.profileError = ""
		return m, func() tea.Msg { dir, err := m.profileActions.Create(m.ctx, name); return profileAddedMsg{dir, err} }
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
		parts = []string{base.Foreground(green).Bold(true).Render("✓ Profile added"), "", dim.Render("Directory created and saved to"), dim.Render("~/.config/husage/profiles.json"), "", base.Render("Log in to this profile from your terminal:"), "", accent.Render(loginCommand(m.createdDirectory)), "", dim.Render("Then press r on the dashboard to load its usage.")}
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
		parts = []string{accent.Bold(true).Render("Add a Claude profile"), "", base.Bold(true).Render("Profile name"), input, "", dim.Render("Letters, numbers, dashes and underscores."), "", dim.Render("Create directory:"), base.Render(safe(directory)), "", dim.Render("Save to ~/.config/husage/profiles.json")}
		if m.savingProfile {
			parts = append(parts, "", accent.Render("Creating profile…"))
		} else if m.profileError != "" {
			parts = append(parts, "", base.Foreground(amber).Render(safe(m.profileError)))
		}
	}
	var lines []string
	for _, part := range parts {
		lines = append(lines, strings.Split(ansi.Hardwrap(part, w, true), "\n")...)
	}
	return lines
}

// Quote paths as literal shell arguments, including homes containing spaces or
// quotes. This command is displayed only, never executed by husage.
func loginCommand(dir string) string {
	quoted := "'" + strings.ReplaceAll(dir, "'", "'\"'\"'") + "'"
	return "env -u CLAUDE_SECURESTORAGE_CONFIG_DIR \\\n  CLAUDE_CONFIG_DIR=" + quoted + " \\\n  claude auth login --claudeai"
}
