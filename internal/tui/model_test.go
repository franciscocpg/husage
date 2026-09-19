package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/subscription"
)

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
	if !strings.Contains(ageText(now, now.Add(time.Hour)), "stale") {
		t.Fatal("stale usage unlabeled")
	}
}
