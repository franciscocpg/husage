package tui

import (
	"context"
	"errors"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/subscription"
)

func TestCardSpacingKeepsDashboardBackground(t *testing.T) {
	accounts, _ := (subscription.Demo{}).Load(context.Background())
	for _, width := range []int{80, 120, 200} {
		rows := base.Width(width).Render(renderAccountRows(accounts, width, time.UTC, time.Now()))
		buffer := uv.NewScreenBuffer(width, lipgloss.Height(rows))
		uv.NewStyledString(rows).Draw(buffer, buffer.Bounds())
		for y := 0; y < buffer.Height(); y++ {
			for x := 0; x < buffer.Width(); x++ {
				cell := buffer.CellAt(x, y)
				if cell.Content != " " {
					continue
				}
				if cell.Style.Bg == nil || color.RGBAModel.Convert(cell.Style.Bg) != color.RGBAModel.Convert(bg) {
					t.Errorf("width %d: space at (%d,%d) exposes the terminal background", width, x, y)
					break
				}
			}
		}
	}
}

func TestFailedRefreshLabelsRecentCachedUsageAsStale(t *testing.T) {
	accounts, _ := (subscription.Demo{}).Load(context.Background())
	a := accounts[0]
	a.Error = "No Claude login found. Sign in again: " + strings.Repeat("CLAUDE_CONFIG_DIR=/saved/profile claude auth login ", 20)
	a.Stale = true
	view := ansi.Strip(renderAccount(a, 100, time.UTC, a.UpdatedAt.Add(time.Minute)))
	for _, text := range []string{"38% used", a.Source + " · stale data · updated 1m ago"} {
		if !strings.Contains(view, text) {
			t.Fatalf("cached usage display missing %q", text)
		}
	}
	if strings.Contains(view, "Sign in again") || strings.Contains(view, "CLAUDE_CONFIG_DIR") || strings.Contains(view, "last successful read") || strings.Count(view, "stale data") != 1 {
		t.Fatal("cached card displayed verbose or duplicate error information", view)
	}
	a.Error = ""
	if lipgloss.Height(view) != lipgloss.Height(renderAccount(a, 100, time.UTC, a.UpdatedAt.Add(time.Minute))) {
		t.Fatal("cached refresh error changed card height")
	}
	a.Stale, a.Error = false, ""
	if strings.Contains(ansi.Strip(renderAccount(a, 100, time.UTC, a.UpdatedAt)), "stale data") {
		t.Fatal("successful refresh retained stale warning")
	}
}

func TestStaleMetadataUsesDashboardTimezone(t *testing.T) {
	loc := time.FixedZone("America/Sao_Paulo", -3*60*60)
	a := subscription.Account{Source: "Claude API", UpdatedAt: time.Date(2026, 9, 20, 6, 39, 0, 0, time.UTC)}
	for _, failed := range []bool{false, true} {
		a.Stale = failed
		view := ansi.Strip(renderAccount(a, 100, loc, a.UpdatedAt.Add(2*time.Hour)))
		if !strings.Contains(view, "Claude API · stale data · updated Sep 20, 3:39am") || strings.Count(view, "stale") != 1 {
			t.Fatal(view)
		}
	}
}

func TestAuthenticationWarningReplacesVerboseError(t *testing.T) {
	accounts, _ := (subscription.Demo{}).Load(context.Background())
	for _, cached := range []bool{true, false} {
		a := accounts[0]
		a.Warning = "Login required. Sign in again for this profile, then press r."
		a.Error = "Missing credentials. Sign in again: " + strings.Repeat("CLAUDE_CONFIG_DIR=/private/profile claude auth login ", 20)
		a.Stale = cached
		if !cached {
			a.Windows = nil
			a.UpdatedAt = time.Time{}
		}
		for _, width := range []int{40, 80, 120} {
			view := ansi.Strip(renderAccount(a, width, time.UTC, time.Now()))
			if !strings.Contains(view, "Login required.") || strings.Contains(view, "CLAUDE_CONFIG_DIR") || strings.Contains(view, "Missing credentials") {
				t.Fatalf("compact warning missing or verbose error leaked: %s", view)
			}
			if lipgloss.Width(view) > width {
				t.Fatalf("warning exceeds card width %d", width)
			}
			if cached && (!strings.Contains(view, "38% used") || strings.Count(view, "stale data") != 1) {
				t.Fatalf("cached usage or stale status lost: %s", view)
			}
		}
	}
}

func TestAccountWithoutCachedUsageStillShowsError(t *testing.T) {
	a := subscription.Account{Source: "Claude API", Error: "Sign in with Claude to load usage."}
	view := ansi.Strip(renderAccount(a, 100, time.UTC, time.Now()))
	if !strings.Contains(view, a.Error) || !strings.Contains(view, "waiting for usage") || strings.Contains(view, "stale data") {
		t.Fatal(view)
	}
}

func TestLayoutFitsTerminalAndKeepsBarsOnOneLine(t *testing.T) {
	a, _ := (subscription.Demo{}).Load(context.Background())
	for _, w := range []int{40, 60, 80, 120} {
		m := New(context.Background(), subscription.Demo{}, time.Minute, time.UTC, true)
		m.accounts = a
		m.loaded = true
		m.loading = false
		m.width = w
		m.height = 24
		view := m.View().Content
		if lines := strings.Split(ansi.Strip(view), "\n"); len(lines) != 24 {
			t.Fatalf("height %d", len(lines))
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > w {
				t.Fatalf("width %d overflow %d", w, ansi.StringWidth(line))
			}
		}
		card := ansi.Strip(renderAccount(a[0], w, time.UTC, time.Now()))
		for _, line := range strings.Split(card, "\n") {
			if strings.Contains(line, "%") && !strings.Contains(line, "% used") {
				t.Fatalf("split usage label at width %d: %q", w, line)
			}
			if strings.Contains(line, "●") && !strings.Contains(line, "● active") {
				t.Fatalf("split active badge at width %d", w)
			}
		}
	}
}

func TestScrollingRefreshAndFailure(t *testing.T) {
	a, _ := (subscription.Demo{}).Load(context.Background())
	m := New(context.Background(), subscription.Demo{}, time.Minute, time.UTC, false)
	m.accounts = a
	m.loaded = true
	m.loading = false
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = next.(Model)
	if m.offset == 0 || !strings.Contains(ansi.Strip(m.View().Content), "Personal") {
		t.Fatal("cannot scroll to second subscription")
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'r'})
	m = next.(Model)
	if cmd == nil || !m.loading {
		t.Fatal("refresh does not load")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'r'})
	if cmd != nil {
		t.Fatal("duplicate in-flight refresh")
	}
	next, _ = m.Update(resultMsg{err: errors.New("temporary failure")})
	m = next.(Model)
	if len(m.accounts) != 2 || m.err == nil || m.loading {
		t.Fatal("failed refresh discarded successful data")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'q'})
	if cmd == nil {
		t.Fatal("quit does not exit")
	}
}

func TestSubscriptionsReflowOnResize(t *testing.T) {
	accounts, _ := (subscription.Demo{}).Load(context.Background())
	accounts[1].Provider = accounts[0].Provider
	m := New(context.Background(), subscription.Demo{}, time.Minute, time.UTC, true)
	m.accounts, m.loaded, m.loading = accounts, true, false
	for _, width := range []int{120, 80, 86, 160, 40} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		m = next.(Model)
		lines := m.bodyLines()
		workRow, personalRow := -1, -1
		bottoms := 0
		for i, line := range lines {
			plain := ansi.Strip(line)
			if strings.Contains(plain, "Acme Team") {
				workRow = i
			}
			if strings.Contains(plain, "Personal · Pro") {
				personalRow = i
			}
			if strings.Count(plain, "╰") == 2 {
				bottoms++
			}
			if ansi.StringWidth(line) > width-4 {
				t.Fatalf("cards overflow width %d", width)
			}
		}
		if workRow < 0 || personalRow < 0 {
			t.Fatalf("subscription missing at width %d", width)
		}
		if width >= 86 && (workRow != personalRow || bottoms != 1) {
			t.Fatalf("cards not side by side with aligned bottoms at width %d", width)
		}
		if width < 86 && workRow >= personalRow {
			t.Fatalf("narrow terminal did not stack cards at width %d", width)
		}
	}
}

func TestExtraSubscriptionsWrapAndRemainScrollable(t *testing.T) {
	accounts, _ := (subscription.Demo{}).Load(context.Background())
	accounts[1].Provider = accounts[0].Provider
	third := accounts[1]
	third.ID, third.Name = "third", "Third subscription"
	third.Error = strings.Repeat("A recoverable usage error. ", 6)
	accounts = append(accounts, third)
	m := New(context.Background(), subscription.Demo{}, time.Minute, time.UTC, true)
	m.accounts, m.loaded, m.loading = accounts, true, false
	m.width = 120
	firstLine, thirdLine := -1, -1
	for i, line := range m.bodyLines() {
		if strings.Contains(line, "Acme Team") {
			firstLine = i
		}
		if strings.Contains(line, "Third subscription") {
			thirdLine = i
		}
		if ansi.StringWidth(line) > m.contentWidth() {
			t.Fatal("wrapped row overflow")
		}
	}
	if firstLine < 0 || thirdLine <= firstLine {
		t.Fatal("extra card did not wrap")
	}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = next.(Model)
	if m.offset == 0 || !strings.Contains(ansi.Strip(m.View().Content), "recoverable usage error") {
		t.Fatal("wrapped subscription inaccessible")
	}
	m.offset = thirdLine
	if !strings.Contains(ansi.Strip(m.View().Content), "Third subscription") {
		t.Fatal("wrapped subscription heading inaccessible")
	}
	snapshot := Snapshot(accounts, 200, time.UTC, time.Now())
	for _, line := range strings.Split(snapshot, "\n") {
		if strings.Contains(line, "Acme Team") && strings.Contains(line, "Personal · Pro") && strings.Contains(line, "Third subscription") {
			return
		}
	}
	t.Fatal("wide snapshot did not put all subscriptions in one row")
}

func TestResetAndUntrustedText(t *testing.T) {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Date(2026, 9, 19, 1, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 9, 19, 3, 20, 0, 0, time.UTC)
	if got := resetText(reset, loc, now); got != "Resets Sep 19 at 12:20am (America/Sao_Paulo)" {
		t.Fatal(got)
	}
	if !strings.Contains(resetText(reset, loc, reset.Add(time.Minute)), "Reset passed") {
		t.Fatal("past reset shown as future")
	}
	if resetText(time.Time{}, loc, now) != "Reset time unavailable" {
		t.Fatal("missing reset fabricated")
	}
	if got := safe("name\x1b[2J\n\a"); got != "name" {
		t.Fatalf("unsafe text: %q", got)
	}
}

func TestProviderSectionsKeepMixedAccountsTogether(t *testing.T) {
	demo, _ := (subscription.Demo{}).Load(context.Background())
	claudeSecond, codexSecond := demo[0], demo[1]
	claudeSecond.Name, claudeSecond.Active = "Second Claude", false
	codexSecond.Name = "Second Codex"
	accounts := []subscription.Account{demo[0], demo[1], claudeSecond, codexSecond}
	for _, width := range []int{40, 80, 120, 200} {
		m := New(context.Background(), subscription.Demo{}, time.Minute, time.UTC, false)
		m.accounts, m.loaded, m.loading = accounts, true, false
		m.width = width
		body := ansi.Strip(strings.Join(m.bodyLines(), "\n"))
		claudeHeader := strings.Index(body, "Claude Code · 2 subscriptions")
		codexHeader := strings.Index(body, "Codex · 2 subscriptions")
		if claudeHeader < 0 || codexHeader <= claudeHeader {
			t.Fatalf("missing or misplaced provider headings at width %d", width)
		}
		for _, name := range []string{"Acme Team", "Second Claude"} {
			if position := strings.Index(body, name); position <= claudeHeader || position >= codexHeader {
				t.Fatalf("%s outside Claude section at width %d", name, width)
			}
		}
		for _, name := range []string{"Personal · Pro", "Second Codex"} {
			if strings.Index(body, name) <= codexHeader {
				t.Fatalf("%s outside Codex section at width %d", name, width)
			}
		}
		for _, line := range m.bodyLines() {
			if ansi.StringWidth(line) > m.contentWidth() {
				t.Fatalf("section overflow at width %d", width)
			}
			if width >= 86 && strings.Contains(line, "Acme Team") && !strings.Contains(line, "Second Claude") {
				t.Fatalf("same-provider cards not side by side at width %d", width)
			}
		}
		if got := ansi.Strip(renderAccounts(accounts, m.contentWidth(), time.UTC, m.now)); got != body {
			t.Fatal("interactive and snapshot layouts differ")
		}
		next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
		if next.(Model).offset == 0 {
			t.Fatal("provider sections did not scroll")
		}
	}
	if accounts[1].Provider != "Codex" {
		t.Fatal("rendering reordered source accounts")
	}
	single := ansi.Strip(renderAccounts(accounts[:1], 80, time.UTC, time.Now()))
	if !strings.Contains(single, "Claude Code · 1 subscription") || strings.Contains(single, "Codex") {
		t.Fatal("single-provider view includes an empty group or incorrect count")
	}
}
