package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/claude"
	"github.com/franciscocpg/husage/internal/profile"
)

func profileModel(t *testing.T) (Model, profile.Store) {
	t.Helper()
	s := profile.Store{Home: t.TempDir()}
	group := claude.NewProfiles(claude.Options{Home: s.Home}, []claude.Options{{Home: s.Home}})
	m := New(context.Background(), group, time.Minute, time.UTC, false).WithProfileActions(&ProfileActions{Directory: s.Directory, Create: func(ctx context.Context, name string) (string, error) {
		dir, err := s.Add(ctx, name)
		if err == nil {
			group.Add(claude.Options{Home: s.Home, ConfigDir: dir})
		}
		return dir, err
	}})
	m.loading = false
	m.loaded = true
	return m, s
}

func profileKey(m Model, code rune, text string) (Model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyPressMsg{Code: code, Text: text})
	return next.(Model), cmd
}

func TestProfileFormCreatesAndRegistersOnlyOnSubmit(t *testing.T) {
	m, s := profileModel(t)
	m, _ = profileKey(m, 'a', "a")
	if !m.profileOpen {
		t.Fatal("form did not open")
	}
	for _, r := range "work" {
		m, _ = profileKey(m, r, string(r))
	}
	if _, err := os.Stat(s.ConfigPath()); !os.IsNotExist(err) {
		t.Fatal("typing wrote config")
	}
	m, cmd := profileKey(m, tea.KeyEnter, "")
	if cmd == nil || !m.savingProfile {
		t.Fatal("submit did not save")
	}
	_, duplicate := profileKey(m, tea.KeyEnter, "")
	if duplicate != nil {
		t.Fatal("duplicate save command")
	}
	next, refresh := m.Update(cmd())
	m = next.(Model)
	if m.createdDirectory != s.Directory("work") || m.savingProfile || refresh == nil {
		t.Fatal("save did not refresh dashboard")
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Profile added") || !strings.Contains(view, "claude auth login") {
		t.Fatal("missing login instructions", view)
	}
	next, _ = m.Update(refresh())
	m = next.(Model)
	if len(m.accounts) != 2 || m.accounts[1].Name != "work" {
		t.Fatal("new profile did not appear", m.accounts)
	}
	m, _ = profileKey(m, tea.KeyEnter, "")
	if m.profileOpen {
		t.Fatal("form did not close")
	}
	b, _ := os.ReadFile(s.ConfigPath())
	var paths []string
	if json.Unmarshal(b, &paths) != nil || len(paths) != 2 || paths[1] != s.Directory("work") {
		t.Fatal("profile not persisted")
	}
}

func TestProfileFormCancelAndValidationDoNotWrite(t *testing.T) {
	m, s := profileModel(t)
	m, _ = profileKey(m, 'a', "a")
	m, cmd := profileKey(m, tea.KeyEnter, "")
	if cmd != nil || m.profileError == "" {
		t.Fatal("empty name accepted")
	}
	m, _ = profileKey(m, 'q', "q")
	if string(m.profileName) != "q" {
		t.Fatal("q quit instead of typing")
	}
	m, _ = profileKey(m, tea.KeyEscape, "")
	if m.profileOpen {
		t.Fatal("escape did not cancel")
	}
	entries, _ := os.ReadDir(s.Home)
	if len(entries) != 0 {
		t.Fatal("cancel created files")
	}
}

func TestProfileFormFailureAndRefreshInFlight(t *testing.T) {
	m, _ := profileModel(t)
	m.profileActions.Create = func(context.Context, string) (string, error) { return "", errors.New("disk full") }
	m, _ = profileKey(m, 'a', "a")
	m, _ = profileKey(m, 'x', "x")
	m, cmd := profileKey(m, tea.KeyEnter, "")
	next, _ := m.Update(cmd())
	m = next.(Model)
	if !m.profileOpen || m.savingProfile || m.profileError != "disk full" {
		t.Fatal("error not shown")
	}
	m.loading = true
	next, cmd = m.Update(profileAddedMsg{directory: "/temporary/profile"})
	m = next.(Model)
	if cmd != nil || !m.reloadAfterAdd {
		t.Fatal("overlapped in-flight refresh")
	}
	next, cmd = m.Update(resultMsg{})
	m = next.(Model)
	if cmd == nil || !m.loading || m.reloadAfterAdd {
		t.Fatal("new profile refresh lost")
	}
}

func TestProfileFormPasteEditingAndLayout(t *testing.T) {
	m, _ := profileModel(t)
	m, _ = profileKey(m, 'a', "a")
	next, _ := m.Update(tea.PasteMsg{Content: "wok\n"})
	m = next.(Model)
	m, _ = profileKey(m, tea.KeyLeft, "")
	m, _ = profileKey(m, 'r', "r")
	if string(m.profileName) != "work" {
		t.Fatal("paste/edit failed", string(m.profileName))
	}
	for _, width := range []int{40, 60, 80} {
		m.width = width
		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatal("form overflow")
			}
		}
	}
	command := loginCommand("/home/it's my/profile")
	if !strings.Contains(command, `'"'"'`) {
		t.Fatal("path not shell-quoted", command)
	}
}

func TestProfileConfirmationScrollsInSmallTerminal(t *testing.T) {
	m, _ := profileModel(t)
	m.profileOpen = true
	m.createdDirectory = "/a/long/profile/directory/personal"
	m.width = 40
	m.height = 14
	m, _ = profileKey(m, tea.KeyPgDown, "")
	if m.profileScroll == 0 {
		t.Fatal("confirmation cannot scroll")
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "claude auth login") {
		t.Fatal("login command inaccessible", ansi.Strip(m.View().Content))
	}
	m, _ = profileKey(m, tea.KeyPgUp, "")
	if m.profileScroll != 0 {
		t.Fatal("cannot scroll back to confirmation title")
	}
}

func TestReadOnlyViewDoesNotOfferProfileCreation(t *testing.T) {
	m, _ := profileModel(t)
	m.profileActions = nil
	m, cmd := profileKey(m, 'a', "a")
	if m.profileOpen || cmd != nil {
		t.Fatal("read-only view offered profile creation")
	}
}
