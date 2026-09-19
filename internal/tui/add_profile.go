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
	Name, Kind string
	Existing   bool // Restore the native login instead of creating a new one.
	ConfigPath string
	Directory  func(string) string
	Command    func(string) string
	Login      func(context.Context, string) tea.ExecCommand
	Register   func(context.Context, string) (string, error)
}

func (a *ProfileActions) configPath() string {
	if a.ConfigPath != "" {
		return a.ConfigPath
	}
	return "~/.config/husage/profiles.json"
}

type profileLoginFinishedMsg struct{ err error }

type profileAddedMsg struct {
	directory string
	err       error
}

func (m Model) WithProfileActions(actions *ProfileActions) Model {
	m.profileActions = actions
	m.profileProviders = []*ProfileActions{actions}
	return m
}

func (m Model) WithProfileProviders(actions []*ProfileActions) Model {
	m.profileProviders = actions
	if len(actions) > 0 {
		m.profileActions = actions[0]
	}
	return m
}

func (a *ProfileActions) name() string {
	if a.Name != "" {
		return a.Name
	}
	return "Claude"
}
func (a *ProfileActions) kind() string {
	if a.Kind != "" {
		return a.Kind
	}
	return "claude"
}
func (a *ProfileActions) command(dir string) string {
	if a.Command != nil {
		return a.Command(dir)
	}
	return profile.LoginCommand(dir)
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
	if m.selectingProvider {
		switch key {
		case "up", "k":
			m.providerIndex = max(0, m.providerIndex-1)
		case "down", "j":
			m.providerIndex = min(len(m.profileProviders)-1, m.providerIndex+1)
		case "enter":
			m.profileActions = m.profileProviders[m.providerIndex]
			m.selectingProvider = false
			m.profileScroll = 0
			return m, nil
		}
		// Keep the highlighted provider visible in short terminals.
		selectedLine := 3 + m.providerIndex
		m.profileScroll = min(m.profileScroll, selectedLine)
		m.profileScroll = max(m.profileScroll, selectedLine-m.bodyHeight()+1)
		return m, nil
	}
	if m.confirmProfile {
		if key != "enter" {
			return m, nil
		}
		m.savingProfile = true
		m.profileError = ""
		m.profileScroll = 0
		if m.loginSucceeded || m.profileActions.Existing {
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
		if err := profile.ValidateName(name); !m.profileActions.Existing && err != nil {
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
	if m.selectingProvider || (m.profileActions != nil && m.profileActions.Existing) {
		return
	}
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
	if m.selectingProvider {
		parts = []string{accent.Bold(true).Render("Add a profile"), dim.Render("Select a provider"), ""}
		for i, actions := range m.profileProviders {
			label := "  " + actions.name()
			if i == m.providerIndex {
				parts = append(parts, accent.Bold(true).Render("› "+actions.name()))
			} else {
				parts = append(parts, base.Render(label))
			}
		}
	} else if m.createdDirectory != "" {
		message := m.profileActions.name() + " login succeeded."
		if m.profileActions.Existing {
			message = "Your existing " + m.profileActions.name() + " account is enabled."
		}
		parts = []string{base.Foreground(green).Bold(true).Render("✓ Profile added"), "", base.Render(message), "", dim.Render("Saved to " + safe(m.profileActions.configPath())), "", base.Render(safe(m.createdDirectory)), "", dim.Render("Your dashboard is refreshing with the new profile.")}
	} else if m.profileActions.Existing {
		parts = []string{accent.Bold(true).Render("Add your existing " + m.profileActions.name() + " account"), ""}
		if !m.confirmProfile {
			parts = append(parts, dim.Render("Provider: "+m.profileActions.name()), "")
		}
		parts = append(parts, base.Render("Use the account already signed in to Cursor CLI."), dim.Render("Your native login and credentials will be kept."), "", dim.Render("Profile list:"), base.Render(safe(m.profileActions.configPath())), "")
		if m.confirmProfile {
			parts = append(parts, accent.Render("Press Enter to add this account to husage."))
		} else {
			parts = append(parts, dim.Render("Press Enter to review."))
		}
		parts = append(parts, "", dim.Render("If you are not signed in, run cursor-agent login, then retry."))
		if m.savingProfile {
			parts = append(parts, "", accent.Render("Adding existing account…"))
		}
	} else if m.confirmProfile {
		parts = []string{accent.Bold(true).Render("Confirm " + m.profileActions.name() + " login"), "", dim.Render("Press Enter to execute this command:"), ""}
		for _, line := range strings.Split(m.profileActions.command(m.profileActions.Directory(string(m.profileName))), "\n") {
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
		directory := "~/.config/husage/" + m.profileActions.kind() + "/<name>"
		if profile.ValidateName(name) == nil {
			directory = m.profileActions.Directory(name)
		}
		parts = []string{accent.Bold(true).Render("Add a " + m.profileActions.name() + " profile"), ""}
		if len(m.profileProviders) > 1 {
			parts = append(parts, dim.Render("Provider: "+m.profileActions.name()), "")
		}
		parts = append(parts, base.Bold(true).Render("Profile name"), input, "", dim.Render("Letters, numbers, dashes and underscores."), "", dim.Render("Profile directory:"), base.Render(safe(directory)), "", dim.Render("Next: review the login command."))
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
