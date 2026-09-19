package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/config"
	"github.com/franciscocpg/husage/internal/subscription"
)

func configModel(t *testing.T) (Model, config.Store) {
	t.Helper()
	s := config.Store{Home: t.TempDir()}
	m := New(context.Background(), subscription.Demo{}, config.DefaultRefresh, time.UTC, true).
		WithConfigActions(&ConfigActions{Save: s.Save})
	m.loading = false
	m.loaded = true
	return m, s
}

func editInterval(m Model, value string) Model {
	m, _ = profileKey(m, 'c', "c")
	next, _ := m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	m = next.(Model)
	next, _ = m.Update(tea.PasteMsg{Content: value})
	return next.(Model)
}

func TestConfigSavePersistsAndReplacesTimer(t *testing.T) {
	m, s := configModel(t)
	m = editInterval(m, "10m")
	oldGeneration := m.tickGeneration
	m, save := profileKey(m, tea.KeyEnter, "")
	if save == nil || !m.savingConfig || m.refresh != config.DefaultRefresh {
		t.Fatal("save did not defer application until persistence")
	}
	if _, duplicate := profileKey(m, tea.KeyEnter, ""); duplicate != nil {
		t.Fatal("duplicate save scheduled")
	}
	next, timer := m.Update(save())
	m = next.(Model)
	if m.configOpen || m.savingConfig || m.refresh != 10*time.Minute || timer == nil {
		t.Fatal("saved interval not applied")
	}
	saved, err := s.Load()
	if err != nil || saved.RefreshInterval != "10m" {
		t.Fatal("interval not persisted", saved, err)
	}
	next, cmd := m.Update(tickMsg{at: time.Now(), generation: oldGeneration})
	m = next.(Model)
	if cmd != nil || m.loading {
		t.Fatal("obsolete timer triggered a reload or rescheduled itself")
	}
	next, cmd = m.Update(tickMsg{at: time.Now(), generation: m.tickGeneration})
	m = next.(Model)
	if cmd == nil || !m.loading {
		t.Fatal("current timer did not trigger reload")
	}
	// When a request is in flight, only the next timer is scheduled.
	_, cmd = m.Update(tickMsg{at: time.Now(), generation: m.tickGeneration})
	if cmd == nil {
		t.Fatal("in-flight reload stopped the timer")
	}
}

func TestConfigCancelValidationAndSaveFailure(t *testing.T) {
	m, _ := configModel(t)
	calls := 0
	m.configActions.Save = func(context.Context, config.Settings) error {
		calls++
		return errors.New("disk full")
	}
	m = editInterval(m, "1s")
	m, cmd := profileKey(m, tea.KeyEnter, "")
	if cmd != nil || m.configError == "" || calls != 0 {
		t.Fatal("invalid interval scheduled a save")
	}
	m, _ = profileKey(m, tea.KeyEscape, "")
	if m.configOpen || m.refresh != config.DefaultRefresh || calls != 0 {
		t.Fatal("cancel changed settings")
	}
	m = editInterval(m, "1h")
	m, cmd = profileKey(m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("save not scheduled")
	}
	next, timer := m.Update(cmd())
	m = next.(Model)
	if !m.configOpen || m.savingConfig || m.configError != "disk full" || m.refresh != config.DefaultRefresh || timer != nil {
		t.Fatal("failed save changed interval or hid error")
	}
	m.configActions.Save = func(context.Context, config.Settings) error { return nil }
	m, cmd = profileKey(m, tea.KeyEnter, "")
	next, timer = m.Update(cmd())
	m = next.(Model)
	if m.refresh != time.Hour || m.configOpen || timer == nil {
		t.Fatal("save retry failed")
	}
}

func TestConfigScreenFitsAndScrolls(t *testing.T) {
	m, _ := configModel(t)
	m, _ = profileKey(m, 'c', "c")
	if !strings.Contains(ansi.Strip(m.View().Content), "Auto-reload interval") {
		t.Fatal("configuration screen missing")
	}
	for _, width := range []int{40, 60, 80} {
		m.width = width
		m.height = 14
		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatal("configuration screen overflow")
			}
		}
		for i := 0; i < 5; i++ {
			m, _ = profileKey(m, tea.KeyPgDown, "")
		}
		if !strings.Contains(ansi.Strip(m.View().Content), "config.json") {
			t.Fatal("configuration help inaccessible")
		}
	}
}
