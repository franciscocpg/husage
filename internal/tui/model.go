package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/subscription"
)

var (
	ink    = lipgloss.Color("#EEEAF7")
	muted  = lipgloss.Color("#9996AC")
	purple = lipgloss.Color("#B4A0FF")
	green  = lipgloss.Color("#8FDCB0")
	amber  = lipgloss.Color("#E6BC78")
	red    = lipgloss.Color("#EF8D9A")
	bg     = lipgloss.Color("#1D1E28")
	base   = lipgloss.NewStyle().Foreground(ink).Background(bg)
	dim    = base.Foreground(muted)
	accent = base.Foreground(purple)
)

type resultMsg struct {
	accounts []subscription.Account
	err      error
}
type tickMsg time.Time

type Model struct {
	provider              subscription.Provider
	ctx                   context.Context
	accounts              []subscription.Account
	err                   error
	loading               bool
	loaded                bool
	demo                  bool
	width, height, offset int
	refresh               time.Duration
	location              *time.Location
	now                   time.Time
}

func New(ctx context.Context, p subscription.Provider, refresh time.Duration, location *time.Location, demo bool) Model {
	return Model{ctx: ctx, provider: p, refresh: refresh, location: location, demo: demo, width: 80, height: 24, loading: true, now: time.Now()}
}

func (m Model) load() tea.Cmd {
	return func() tea.Msg { a, e := m.provider.Load(m.ctx); return resultMsg{a, e} }
}
func (m Model) tick() tea.Cmd {
	return tea.Tick(m.refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}
func (m Model) Init() tea.Cmd { return tea.Batch(m.load(), m.tick()) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "r":
			if !m.loading {
				m.loading = true
				return m, m.load()
			}
		case "j", "down":
			m.offset++
		case "k", "up":
			m.offset = max(0, m.offset-1)
		case "pgdown", "ctrl+f", "space":
			m.offset += max(1, m.height-7)
		case "pgup", "ctrl+b":
			m.offset = max(0, m.offset-max(1, m.height-7))
		case "home", "g":
			m.offset = 0
		case "end", "G":
			m.offset = 1 << 20
		}
	case resultMsg:
		m.loading = false
		m.loaded = true
		m.err = msg.err
		m.now = time.Now()
		if msg.err == nil {
			m.accounts = msg.accounts
		}
	case tickMsg:
		m.now = time.Time(msg)
		if !m.loading {
			m.loading = true
			return m, tea.Batch(m.load(), m.tick())
		}
		return m, m.tick()
	}
	m.offset = min(m.offset, max(0, len(m.bodyLines())-m.bodyHeight()))
	return m, nil
}

func (m Model) bodyHeight() int   { return max(1, m.height-7) }
func (m Model) contentWidth() int { return max(1, min(90, m.width-4)) }

func (m Model) bodyLines() []string {
	w := m.contentWidth()
	var parts []string
	if m.err != nil {
		parts = append(parts, base.Foreground(amber).Width(w).Render("Unable to refresh · "+safe(m.err.Error())+"\nShowing the last successful read, if available.\n"))
	}
	if !m.loaded {
		parts = append(parts, dim.Render("Reading your Claude subscriptions…"))
	} else if len(m.accounts) == 0 && m.err == nil {
		parts = append(parts, accent.Bold(true).Render("Your subscriptions, in one place."), "", dim.Width(w).Render("No Claude subscription found.\n\nOpen Claude Code and use /login, then press r.\n\nRepeat --claude-dir to monitor multiple Claude configurations."))
	} else {
		for _, a := range m.accounts {
			parts = append(parts, renderAccount(a, w, m.location, m.now), "")
		}
	}
	return strings.Split(strings.Join(parts, "\n"), "\n")
}

func (m Model) View() tea.View {
	w := m.contentWidth()
	header := accent.Bold(true).Render("◈ husage") + dim.Render("  /  your coding subscriptions")
	status := "Claude Code"
	if m.demo {
		status += "  ·  DEMO"
	}
	status += fmt.Sprintf("  ·  %d subscriptions", len(m.accounts))
	if m.loading {
		status += "  ·  refreshing…"
	}
	lines := m.bodyLines()
	h := m.bodyHeight()
	start := min(m.offset, max(0, len(lines)-h))
	end := min(len(lines), start+h)
	visible := append([]string{}, lines[start:end]...)
	for len(visible) < h {
		visible = append(visible, "")
	}
	footer := "r refresh   ↑↓ scroll   q quit"
	if len(lines) > h {
		footer += fmt.Sprintf("   %d–%d/%d", start+1, end, len(lines))
	}
	content := []string{header, dim.Render(status), ""}
	content = append(content, visible...)
	content = append(content, "", dim.Render(footer), "")
	for i, line := range content {
		content[i] = "  " + ansi.Truncate(line, w, "")
	}
	if len(content) > m.height {
		content = content[:m.height]
	}
	v := tea.NewView(base.Width(m.width).Height(m.height).Render(strings.Join(content, "\n")))
	v.AltScreen = true
	return v
}

func renderAccount(a subscription.Account, width int, loc *time.Location, now time.Time) string {
	inner := max(1, width-6)
	border := lipgloss.Color("#414052")
	if a.Active {
		border = lipgloss.Color("#7F70AC")
	}
	name := base.Bold(true).Render(safe(a.Name))
	badge := dim.Render("saved")
	if a.Active {
		badge = base.Foreground(green).Render("● active")
	}
	gap := inner - lipgloss.Width(name) - lipgloss.Width(badge)
	heading := name + "\n" + badge
	if gap >= 2 {
		heading = name + strings.Repeat(" ", gap) + badge
	}
	parts := []string{heading, dim.Render(safe(a.Email)), ""}
	for i, w := range a.Windows {
		if i > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, base.Bold(true).Render(safe(w.Label)), renderBar(w.Used, inner), dim.Render(resetText(w.ResetsAt, loc, now)))
	}
	if a.Error != "" {
		parts = append(parts, "", base.Foreground(amber).Width(inner).Render(safe(a.Error)))
	}
	metadata := safe(a.Source) + " · " + ageText(a.UpdatedAt, now)
	metaStyle := dim
	if a.UpdatedAt.IsZero() || now.Sub(a.UpdatedAt) > 10*time.Minute {
		metaStyle = base.Foreground(amber)
	}
	parts = append(parts, metaStyle.Render(metadata))
	return base.Border(lipgloss.RoundedBorder()).BorderForeground(border).BorderBackground(bg).Padding(0, 2).Width(width).Render(strings.Join(parts, "\n"))
}

func renderBar(used float64, width int) string {
	if math.IsNaN(used) || math.IsInf(used, 0) {
		return dim.Render("Usage unavailable")
	}
	used = math.Max(0, math.Min(100, used))
	label := fmt.Sprintf(" %3.0f%% used", used)
	w := max(1, width-len(label))
	filled := min(w, int(math.Round(used/100*float64(w))))
	color := purple
	if used >= 90 {
		color = red
	} else if used >= 75 {
		color = amber
	}
	return base.Foreground(color).Render(strings.Repeat("█", filled)) + base.Foreground(lipgloss.Color("#45415E")).Render(strings.Repeat("░", w-filled)) + base.Render(label)
}

func resetText(t time.Time, loc *time.Location, now time.Time) string {
	if t.IsZero() {
		return "Reset time unavailable"
	}
	local := t.In(loc)
	zone := loc.String()
	if zone == "Local" {
		zone, _ = local.Zone()
	}
	format := "Jan 2 at 3:04pm"
	if local.Format("2006-01-02") == now.In(loc).Format("2006-01-02") {
		format = "3:04pm"
	}
	prefix := "Resets "
	if !t.After(now) {
		prefix = "Reset passed · "
	}
	return prefix + local.Format(format) + " (" + zone + ")"
}

func ageText(t, now time.Time) string {
	if t.IsZero() {
		return "waiting for usage"
	}
	d := max(time.Duration(0), now.Sub(t))
	if d < time.Minute {
		return "updated just now"
	}
	if d < 10*time.Minute {
		return fmt.Sprintf("updated %dm ago", int(d.Minutes()))
	}
	if d < time.Hour {
		return fmt.Sprintf("stale · updated %dm ago", int(d.Minutes()))
	}
	return "stale · updated " + t.Local().Format("Jan 2, 3:04pm")
}

// Account names and remote fields are data, never terminal control sequences.
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}

// Snapshot renders the same cards without starting an interactive terminal.
func Snapshot(accounts []subscription.Account, width int, loc *time.Location, now time.Time) string {
	parts := []string{accent.Bold(true).Render("◈ husage") + dim.Render("  /  Claude Code"), ""}
	for _, a := range accounts {
		parts = append(parts, renderAccount(a, max(20, width), loc, now), "")
	}
	if len(accounts) == 0 {
		parts = append(parts, "No Claude subscription found. Open Claude Code and use /login.")
	}
	return strings.Join(parts, "\n")
}
