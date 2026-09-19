package tui

import (
	"context"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/config"
)

type ConfigActions struct {
	Save func(context.Context, config.Settings) error
}

type configSavedMsg struct {
	settings config.Settings
	err      error
}

func (m Model) WithConfigActions(actions *ConfigActions) Model {
	m.configActions = actions
	return m
}

func (m Model) updateConfigKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.savingConfig {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.configOpen = false
		return m, nil
	case "enter":
		settings := config.Settings{RefreshInterval: strings.TrimSpace(string(m.configInput))}
		if _, err := settings.Interval(); err != nil {
			m.configError = err.Error()
			m.configScroll = 1 << 20
			return m, nil
		}
		m.savingConfig = true
		m.configError = ""
		return m, func() tea.Msg {
			return configSavedMsg{settings: settings, err: m.configActions.Save(m.ctx, settings)}
		}
	case "pgdown":
		m.configScroll = min(max(0, len(m.configLines())-m.bodyHeight()), m.configScroll+m.bodyHeight())
		return m, nil
	case "pgup":
		m.configScroll = max(0, min(m.configScroll, max(0, len(m.configLines())-m.bodyHeight()))-m.bodyHeight())
		return m, nil
	case "backspace":
		if m.configCursor > 0 {
			m.configInput = append(m.configInput[:m.configCursor-1], m.configInput[m.configCursor:]...)
			m.configCursor--
		}
	case "delete":
		if m.configCursor < len(m.configInput) {
			m.configInput = append(m.configInput[:m.configCursor], m.configInput[m.configCursor+1:]...)
		}
	case "left":
		m.configCursor = max(0, m.configCursor-1)
	case "right":
		m.configCursor = min(len(m.configInput), m.configCursor+1)
	case "home", "ctrl+a":
		m.configCursor = 0
	case "end", "ctrl+e":
		m.configCursor = len(m.configInput)
	case "ctrl+u":
		m.configInput = nil
		m.configCursor = 0
	default:
		m.insertConfigText(msg.Text)
	}
	m.configScroll = 0
	m.configError = ""
	return m, nil
}

func (m *Model) insertConfigText(text string) {
	for _, r := range text {
		if unicode.IsControl(r) || len(m.configInput) >= 32 {
			continue
		}
		next := append([]rune{}, m.configInput[:m.configCursor]...)
		next = append(next, r)
		next = append(next, m.configInput[m.configCursor:]...)
		m.configInput = next
		m.configCursor++
	}
}

func (m Model) configLines() []string {
	input := accent.Render(string(m.configInput[:m.configCursor])) + base.Reverse(true).Render(" ") + accent.Render(string(m.configInput[m.configCursor:]))
	parts := []string{
		accent.Bold(true).Render("Configuration"), "",
		base.Bold(true).Render("Auto-reload interval"), input, "",
		dim.Render("Examples: 30s, 5m, 10m, 1h. Default: 5m."),
		dim.Render("Minimum: 5s. Ctrl+U clears the field."), "",
		dim.Render("Claude usage requests remain limited to once per 5 minutes."), "",
		dim.Render("Enter saves and applies immediately. Esc discards changes."),
		dim.Render("Saved in:"), dim.Render("~/.config/husage/config.json"),
	}
	if m.configError != "" {
		parts = append(parts, "", base.Foreground(amber).Render(safe(m.configError)))
	}
	var lines []string
	for _, part := range parts {
		lines = append(lines, strings.Split(ansi.Hardwrap(part, m.contentWidth(), true), "\n")...)
	}
	return lines
}
