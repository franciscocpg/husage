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
type tickMsg struct {
	at         time.Time
	generation uint64
}

type Model struct {
	provider                                   subscription.Provider
	ctx                                        context.Context
	accounts                                   []subscription.Account
	err                                        error
	loading                                    bool
	loaded                                     bool
	demo                                       bool
	width, height, offset                      int
	refresh                                    time.Duration
	tickGeneration                             uint64
	location                                   *time.Location
	now                                        time.Time
	profileActions                             *ProfileActions
	profileProviders                           []*ProfileActions
	selectingProvider                          bool
	providerIndex                              int
	profileOpen, savingProfile, reloadAfterAdd bool
	profileName                                []rune
	profileCursor, profileScroll               int
	profileError, createdDirectory             string
	confirmProfile, loginSucceeded             bool
	profileLogin                               tea.ExecCommand
	configActions                              *ConfigActions
	configOpen, savingConfig                   bool
	configInput                                []rune
	configCursor, configScroll                 int
	configError                                string
	removeActions                              *RemoveActions
	removeOpen, removeConfirm, removing        bool
	removeIndex, removeScroll                  int
	removeChoices                              []subscription.Account
	removeError                                string
	removed                                    map[string]bool
}

func New(ctx context.Context, p subscription.Provider, refresh time.Duration, location *time.Location, demo bool) Model {
	return Model{ctx: ctx, provider: p, refresh: refresh, location: location, demo: demo, width: 80, height: 24, loading: true, now: time.Now()}
}

func (m Model) load() tea.Cmd {
	return func() tea.Msg { a, e := m.provider.Load(m.ctx); return resultMsg{a, e} }
}
func (m Model) tick() tea.Cmd {
	return tea.Tick(m.refresh, func(t time.Time) tea.Msg { return tickMsg{at: t, generation: m.tickGeneration} })
}
func (m Model) Init() tea.Cmd { return tea.Batch(m.load(), m.tick()) }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
	case tea.KeyPressMsg:
		if m.removeOpen {
			return m.updateRemoveKey(msg)
		}
		if m.configOpen {
			return m.updateConfigKey(msg)
		}
		if m.profileOpen {
			return m.updateProfileKey(msg)
		}
		switch msg.String() {
		case "d":
			if m.removeActions != nil {
				m.removeOpen, m.removeConfirm = true, false
				m.removeIndex, m.removeScroll, m.removeError = 0, 0, ""
				m.removeChoices = append([]subscription.Account{}, m.accounts...)
			}
		case "c":
			if m.configActions != nil {
				m.configOpen = true
				m.configInput = []rune(m.refresh.String())
				m.configCursor = len(m.configInput)
				m.configScroll = 0
				m.configError = ""
			}
		case "a":
			if m.profileActions != nil {
				m.profileOpen = true
				m.selectingProvider = true
				m.providerIndex = 0
				m.profileName = nil
				m.profileCursor = 0
				m.profileScroll = 0
				m.profileError = ""
				m.createdDirectory = ""
				m.confirmProfile = false
				m.loginSucceeded = false
				m.profileLogin = nil
			}
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
			m.accounts = m.withoutRemoved(msg.accounts)
		}
		if m.reloadAfterAdd {
			m.reloadAfterAdd = false
			m.loading = true
			return m, m.load()
		}
	case tea.PasteMsg:
		if m.configOpen && !m.savingConfig {
			m.insertConfigText(msg.Content)
			m.configScroll = 0
			m.configError = ""
		}
		if m.profileOpen && !m.confirmProfile && !m.savingProfile && m.createdDirectory == "" {
			m.insertProfileText(msg.Content)
		}
	case profileLoginFinishedMsg:
		if msg.err != nil {
			m.savingProfile = false
			m.profileError = msg.err.Error()
			m.profileScroll = 1 << 20
			return m, nil
		}
		m.loginSucceeded = true
		return m, m.registerProfile()
	case profileAddedMsg:
		m.savingProfile = false
		if msg.err != nil {
			m.profileError = msg.err.Error()
			m.profileScroll = 1 << 20
			return m, nil
		}
		m.createdDirectory = msg.directory
		m.removed = nil
		m.profileScroll = 0
		if m.loading {
			m.reloadAfterAdd = true
		} else {
			m.loading = true
			return m, m.load()
		}
	case removedMsg:
		m.removing = false
		if msg.err != nil {
			m.removeError = msg.err.Error()
			m.removeScroll = 1 << 20
			return m, nil
		}
		if m.removed == nil {
			m.removed = map[string]bool{}
		}
		m.removed[accountKey(msg.account)] = true
		m.accounts = m.withoutRemoved(m.accounts)
		m.removeOpen, m.removeConfirm = false, false
		m.offset = 0
		if m.loading {
			m.reloadAfterAdd = true
		} else {
			m.loading = true
			return m, m.load()
		}
	case configSavedMsg:
		m.savingConfig = false
		if msg.err != nil {
			m.configError = msg.err.Error()
			m.configScroll = 1 << 20
			return m, nil
		}
		m.refresh, _ = msg.settings.Interval()
		m.configOpen = false
		// A previous timer may still fire. Its generation must not reload or
		// schedule another timer after the interval changes.
		m.tickGeneration++
		return m, m.tick()
	case tickMsg:
		if msg.generation != m.tickGeneration {
			return m, nil
		}
		m.now = msg.at
		if !m.loading {
			m.loading = true
			return m, tea.Batch(m.load(), m.tick())
		}
		return m, m.tick()
	}
	m.offset = min(m.offset, max(0, len(m.bodyLines())-m.bodyHeight()))
	return m, nil
}

func (m Model) bodyHeight() int { return max(1, m.height-7) }
func (m Model) contentWidth() int {
	w := max(1, m.width-4)
	if m.profileOpen || m.configOpen || m.removeOpen {
		return min(90, w)
	}
	return w
}

func (m Model) bodyLines() []string {
	if m.removeOpen {
		return m.removeLines()
	}
	if m.configOpen {
		return m.configLines()
	}
	if m.profileOpen {
		return m.profileLines()
	}
	w := m.contentWidth()
	var parts []string
	if m.err != nil {
		parts = append(parts, base.Foreground(amber).Width(w).Render("Unable to refresh · "+safe(m.err.Error())+"\nShowing the last successful read, if available.\n"))
	}
	if !m.loaded {
		parts = append(parts, dim.Render("Reading your subscriptions…"))
	} else if len(m.accounts) == 0 && m.err == nil {
		help := "No subscription found.\n\nSign in with Claude Code, Codex or Cursor CLI, then press r.\n\nUse --claude-dir or --codex-home for additional profiles."
		if m.profileActions != nil {
			help = "No subscription found.\n\nPress a to add a Claude, Codex or existing Cursor profile.\n\nAlready logged in? Press r to refresh."
		}
		parts = append(parts, accent.Bold(true).Render("Your subscriptions, in one place."), "", dim.Width(w).Render(help))
	} else {
		parts = append(parts, renderAccounts(m.accounts, w, m.location, m.now))
	}
	return strings.Split(strings.Join(parts, "\n"), "\n")
}

func (m Model) View() tea.View {
	w := m.contentWidth()
	header := accent.Bold(true).Render("◈ husage") + dim.Render("  /  your coding subscriptions")
	status := "Claude Code · Codex · Cursor"
	if m.demo {
		status += "  ·  DEMO"
	}
	status += fmt.Sprintf("  ·  %d subscriptions", len(m.accounts))
	status += "  ·  reload " + m.refresh.String()
	if m.loading {
		status += "  ·  refreshing…"
	}
	lines := m.bodyLines()
	h := m.bodyHeight()
	start := min(m.offset, max(0, len(lines)-h))
	if m.profileOpen {
		start = min(m.profileScroll, max(0, len(lines)-h))
	}
	if m.configOpen {
		start = min(m.configScroll, max(0, len(lines)-h))
	}
	if m.removeOpen {
		start = min(m.removeScroll, max(0, len(lines)-h))
	}
	end := min(len(lines), start+h)
	visible := append([]string{}, lines[start:end]...)
	for len(visible) < h {
		visible = append(visible, "")
	}
	footer := "r refresh   ↑↓ scroll   q quit"
	if m.configActions != nil {
		footer = "c config   " + footer
	}
	if m.removeActions != nil {
		footer = "d remove profile   " + footer
	}
	if m.profileActions != nil {
		footer = "a add profile   " + footer
	}
	if m.profileOpen {
		footer = "enter review command   esc cancel"
		if m.confirmProfile {
			footer = "enter execute login   esc cancel"
		}
		if m.loginSucceeded {
			footer = "enter save profile   esc cancel"
		}
		if m.savingProfile {
			footer = "Completing profile setup…"
		}
		if m.createdDirectory != "" {
			footer = "enter return to dashboard"
		}
		if m.profileActions.Existing && m.createdDirectory == "" {
			footer = "enter review   esc cancel"
			if m.confirmProfile {
				footer = "enter add profile   esc cancel"
			}
			if m.savingProfile {
				footer = "Adding existing account…"
			}
		}
		if len(lines) > h && !m.savingProfile {
			footer = "enter continue  esc cancel  pg↑↓ scroll"
			if m.createdDirectory != "" {
				footer = "enter done  pg↑↓ scroll"
			}
		}
		if m.selectingProvider {
			footer = "↑↓ select   enter continue   esc cancel"
		}
	}
	if m.configOpen {
		footer = "enter save   esc cancel"
		if len(lines) > h {
			footer += "   pg↑↓ scroll"
		}
		if m.savingConfig {
			footer = "Saving configuration…"
		}
	}
	if m.removeOpen {
		footer = "↑↓ select  enter review  esc cancel"
		if m.removeConfirm {
			footer = "enter remove  esc cancel"
		}
		if m.removing {
			footer = "Removing subscription…"
		} else if len(lines) > h {
			footer += "  pg↑↓ scroll"
		}
	}
	if len(lines) > h && !m.profileOpen && !m.configOpen && !m.removeOpen {
		footer += fmt.Sprintf("   %d–%d/%d", start+1, end, len(lines))
	}
	content := []string{header, dim.Render(status), ""}
	content = append(content, visible...)
	content = append(content, "", renderShortcuts(footer), "")
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

func renderShortcuts(text string) string {
	words := strings.Split(text, " ")
	for i, word := range words {
		switch word {
		case "a", "c", "d", "r", "q", "↑↓", "enter", "esc", "tab", "pg↑↓":
			words[i] = accent.Bold(true).Render(word)
		default:
			words[i] = dim.Render(word)
		}
	}
	return strings.Join(words, dim.Render(" "))
}

// renderAccounts keeps providers in first-seen order and preserves the account
// order within each provider, including the active account's leading position.
func renderAccounts(accounts []subscription.Account, width int, loc *time.Location, now time.Time) string {
	var providers []string
	groups := make(map[string][]subscription.Account)
	for _, account := range accounts {
		provider := account.Provider
		if _, exists := groups[provider]; !exists {
			providers = append(providers, provider)
		}
		groups[provider] = append(groups[provider], account)
	}
	var sections []string
	for _, provider := range providers {
		group := groups[provider]
		label := safe(provider)
		if label == "" {
			label = "Other subscriptions"
		}
		count := fmt.Sprintf("%d subscriptions", len(group))
		if len(group) == 1 {
			count = "1 subscription"
		}
		heading := accent.Bold(true).Render(label) + dim.Render(" · "+count)
		heading = ansi.Truncate(heading, max(1, width), "")
		if remaining := width - lipgloss.Width(heading) - 2; remaining > 0 {
			heading += dim.Render("  " + strings.Repeat("─", remaining))
		}
		sections = append(sections, heading+"\n\n"+renderAccountRows(group, width, loc, now))
	}
	return strings.Join(sections, "\n\n")
}

// Each provider's cards fill rows independently and wrap on narrow terminals.
func renderAccountRows(accounts []subscription.Account, width int, loc *time.Location, now time.Time) string {
	if len(accounts) == 0 {
		return ""
	}
	const minCardWidth, gap = 40, 2
	columns := min(len(accounts), max(1, (width+gap)/(minCardWidth+gap)))
	cardWidth := max(1, (width-gap*(columns-1))/columns)
	var rows []string
	for start := 0; start < len(accounts); start += columns {
		row := accounts[start:min(start+columns, len(accounts))]
		height := 0
		for _, a := range row {
			height = max(height, lipgloss.Height(renderAccount(a, cardWidth, loc, now)))
		}
		var cards []string
		for i, a := range row {
			if i > 0 {
				// JoinHorizontal pads short blocks with unstyled spaces. Give
				// the spacer the full row height to retain our background.
				cards = append(cards, base.Width(gap).Height(height).Render(""))
			}
			cards = append(cards, renderAccountSized(a, cardWidth, height, loc, now))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cards...))
	}
	return strings.Join(rows, "\n\n")
}

func renderAccount(a subscription.Account, width int, loc *time.Location, now time.Time) string {
	return renderAccountSized(a, width, 0, loc, now)
}

func renderAccountSized(a subscription.Account, width, height int, loc *time.Location, now time.Time) string {
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
		// Nested styles reset ANSI colors; the gap needs its own background.
		heading = name + base.Render(strings.Repeat(" ", gap)) + badge
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
	if a.Stale {
		parts = append(parts, base.Foreground(amber).Width(inner).Render("Stale data · showing the last successful read."))
	}
	metadata := safe(a.Source) + " · " + ageText(a.UpdatedAt, now)
	metaStyle := dim
	if a.Stale || a.UpdatedAt.IsZero() || now.Sub(a.UpdatedAt) > 10*time.Minute {
		metaStyle = base.Foreground(amber)
	}
	parts = append(parts, metaStyle.Render(metadata))
	return base.Border(lipgloss.RoundedBorder()).BorderForeground(border).BorderBackground(bg).Padding(0, 2).Width(width).Height(height).Render(strings.Join(parts, "\n"))
}

func renderBar(used float64, width int) string {
	if math.IsNaN(used) || math.IsInf(used, 0) {
		return dim.Render("Usage unavailable")
	}
	used = math.Max(0, used)
	label := fmt.Sprintf(" %3.0f%% used", used)
	w := max(1, width-len(label))
	filled := min(w, int(math.Round(math.Min(100, used)/100*float64(w))))
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
	parts := []string{accent.Bold(true).Render("◈ husage") + dim.Render("  /  Claude Code · Codex · Cursor"), ""}
	parts = append(parts, renderAccounts(accounts, max(20, width), loc, now))
	if len(accounts) == 0 {
		parts = append(parts, "No subscription found. Sign in with Claude Code, Codex or Cursor CLI.")
	}
	return strings.Join(parts, "\n")
}
