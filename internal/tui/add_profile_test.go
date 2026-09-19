package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"encoding/json"
	"errors"
	"github.com/charmbracelet/x/ansi"
	"github.com/franciscocpg/husage/internal/claude"
	"github.com/franciscocpg/husage/internal/codexcli"
	"github.com/franciscocpg/husage/internal/profile"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeProfileLogin struct{ run func() error }

func (f *fakeProfileLogin) Run() error          { return f.run() }
func (f *fakeProfileLogin) SetStdin(io.Reader)  {}
func (f *fakeProfileLogin) SetStdout(io.Writer) {}
func (f *fakeProfileLogin) SetStderr(io.Writer) {}

func profileModel(t *testing.T) (Model, profile.Store) {
	t.Helper()
	s := profile.Store{Home: t.TempDir()}
	group := claude.NewProfiles(claude.Options{Home: s.Home}, []claude.Options{{Home: s.Home}})
	m := New(context.Background(), group, time.Minute, time.UTC, false).WithProfileActions(&ProfileActions{
		Directory: s.Directory,
		Login: func(ctx context.Context, name string) tea.ExecCommand {
			return &fakeProfileLogin{run: func() error { _, err := s.Prepare(ctx, name); return err }}
		},
		Register: func(ctx context.Context, name string) (string, error) {
			dir, err := s.Register(ctx, name)
			if err == nil {
				group.Add(claude.Options{Home: s.Home, ConfigDir: dir})
			}
			return dir, err
		},
	})
	m.loading = false
	m.loaded = true
	return m, s
}
func profileKey(m Model, code rune, text string) (Model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyPressMsg{Code: code, Text: text})
	return next.(Model), cmd
}
func nameProfile(m Model, name string) Model {
	m, _ = profileKey(m, 'a', "a")
	for _, r := range name {
		m, _ = profileKey(m, r, string(r))
	}
	return m
}

func TestProfileRequiresSecondEnterAndSuccessfulLoginBeforeRegistering(t *testing.T) {
	m, s := profileModel(t)
	m = nameProfile(m, "work")
	m, cmd := profileKey(m, tea.KeyEnter, "")
	if cmd != nil || !m.confirmProfile || m.savingProfile || m.profileLogin != nil {
		t.Fatal("first Enter started execution")
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Confirm Claude login") || !strings.Contains(view, "claude auth login") {
		t.Fatal("command not previewed", view)
	}
	entries, _ := os.ReadDir(s.Home)
	if len(entries) != 0 {
		t.Fatal("preview wrote files")
	}
	m, cmd = profileKey(m, tea.KeyEnter, "")
	if cmd == nil || !m.savingProfile || m.profileLogin == nil {
		t.Fatal("second Enter did not schedule login")
	}
	_, duplicate := profileKey(m, tea.KeyEnter, "")
	if duplicate != nil {
		t.Fatal("duplicate login command")
	}
	// Bubble Tea runs this command with the terminal released, then sends its exit result.
	if err := m.profileLogin.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.ConfigPath()); !os.IsNotExist(err) {
		t.Fatal("registered before successful exit callback")
	}
	next, save := m.Update(profileLoginFinishedMsg{})
	m = next.(Model)
	if save == nil || !m.loginSucceeded {
		t.Fatal("success did not schedule registration")
	}
	next, refresh := m.Update(save())
	m = next.(Model)
	if m.createdDirectory != s.Directory("work") || m.savingProfile || refresh == nil {
		t.Fatal("profile not saved")
	}
	next, _ = m.Update(refresh())
	m = next.(Model)
	if len(m.accounts) != 2 || m.accounts[1].Name != "work" {
		t.Fatal("profile not added to dashboard", m.accounts)
	}
	b, _ := os.ReadFile(s.ConfigPath())
	var paths []string
	if json.Unmarshal(b, &paths) != nil || len(paths) != 2 || paths[1] != s.Directory("work") {
		t.Fatal("profile not persisted")
	}
	m, _ = profileKey(m, tea.KeyEnter, "")
	if m.profileOpen {
		t.Fatal("confirmation did not close")
	}
}

func TestCancellingPreviewDoesNotExecuteOrWrite(t *testing.T) {
	m, s := profileModel(t)
	m = nameProfile(m, "work")
	m, _ = profileKey(m, tea.KeyEnter, "")
	m, cmd := profileKey(m, tea.KeyEscape, "")
	if m.profileOpen || cmd != nil || m.profileLogin != nil {
		t.Fatal("escape did not cancel preview")
	}
	entries, _ := os.ReadDir(s.Home)
	if len(entries) != 0 {
		t.Fatal("cancel wrote files")
	}
	m = nameProfile(m, "")
	m, cmd = profileKey(m, tea.KeyEnter, "")
	if cmd != nil || m.profileError == "" || m.confirmProfile {
		t.Fatal("empty name accepted")
	}
	m, _ = profileKey(m, 'q', "q")
	if string(m.profileName) != "q" {
		t.Fatal("q quit instead of typing")
	}
}

func TestLoginFailureDoesNotRegisterAndAllowsRetry(t *testing.T) {
	m, s := profileModel(t)
	calls := 0
	m.profileActions.Login = func(context.Context, string) tea.ExecCommand {
		return &fakeProfileLogin{run: func() error { calls++; return errors.New("cancelled login") }}
	}
	m = nameProfile(m, "work")
	m, _ = profileKey(m, tea.KeyEnter, "")
	m, _ = profileKey(m, tea.KeyEnter, "")
	err := m.profileLogin.Run()
	next, save := m.Update(profileLoginFinishedMsg{err: err})
	m = next.(Model)
	if save != nil || m.loginSucceeded || m.savingProfile || !m.confirmProfile || m.profileError == "" {
		t.Fatal("failed login saved or hid failure")
	}
	if _, err := os.Stat(s.ConfigPath()); !os.IsNotExist(err) {
		t.Fatal("failed login registered")
	}
	same := m.profileLogin
	m, cmd := profileKey(m, tea.KeyEnter, "")
	if cmd == nil || m.profileLogin != same || calls != 1 {
		t.Fatal("retry did not reuse prepared login")
	}
}

func TestFailedLoginCanBeRetriedAfterReopeningForm(t *testing.T) {
	m, s := profileModel(t)
	loginFactory := m.profileActions.Login
	m.profileActions.Login = func(ctx context.Context, name string) tea.ExecCommand {
		return &fakeProfileLogin{run: func() error {
			if _, err := s.Prepare(ctx, name); err != nil {
				return err
			}
			return errors.New("cancelled login")
		}}
	}
	m = nameProfile(m, "work")
	m, _ = profileKey(m, tea.KeyEnter, "")
	m, _ = profileKey(m, tea.KeyEnter, "")
	next, save := m.Update(profileLoginFinishedMsg{err: m.profileLogin.Run()})
	m = next.(Model)
	if save != nil {
		t.Fatal("failed login scheduled registration")
	}
	m, _ = profileKey(m, tea.KeyEscape, "")
	m.profileActions.Login = loginFactory
	m = nameProfile(m, "work")
	m, _ = profileKey(m, tea.KeyEnter, "")
	if m.profileLogin != nil {
		t.Fatal("reopening reused old runner")
	}
	m, _ = profileKey(m, tea.KeyEnter, "")
	if err := m.profileLogin.Run(); err != nil {
		t.Fatal("leftover directory blocked retry", err)
	}
	if _, err := os.Stat(s.ConfigPath()); !os.IsNotExist(err) {
		t.Fatal("registered before success callback")
	}
	next, save = m.Update(profileLoginFinishedMsg{})
	m = next.(Model)
	if save == nil {
		t.Fatal("successful retry did not schedule registration")
	}
	next, _ = m.Update(save())
	m = next.(Model)
	if m.createdDirectory != s.Directory("work") || m.profileError != "" {
		t.Fatal("retry did not save profile", m.profileError)
	}
	data, err := os.ReadFile(s.ConfigPath())
	var paths []string
	if err != nil || json.Unmarshal(data, &paths) != nil || len(paths) != 2 || paths[1] != s.Directory("work") {
		t.Fatal("successful retry was not persisted", err)
	}
}

func TestSaveFailureRetriesWithoutAnotherLoginAndQueuesRefresh(t *testing.T) {
	m, _ := profileModel(t)
	m.profileActions.Register = func(context.Context, string) (string, error) { return "", errors.New("disk full") }
	m = nameProfile(m, "work")
	m, _ = profileKey(m, tea.KeyEnter, "")
	m, _ = profileKey(m, tea.KeyEnter, "")
	next, save := m.Update(profileLoginFinishedMsg{})
	m = next.(Model)
	next, _ = m.Update(save())
	m = next.(Model)
	if !m.loginSucceeded || m.savingProfile || m.profileError != "disk full" {
		t.Fatal("save error not shown")
	}
	m, save = profileKey(m, tea.KeyEnter, "")
	if save == nil {
		t.Fatal("save retry missing")
	}
	if _, ok := save().(profileAddedMsg); !ok {
		t.Fatal("save retry started login again")
	}
	m.loading = true
	next, cmd := m.Update(profileAddedMsg{directory: "/temporary/profile"})
	m = next.(Model)
	if cmd != nil || !m.reloadAfterAdd {
		t.Fatal("overlapped refresh")
	}
	next, cmd = m.Update(resultMsg{})
	m = next.(Model)
	if cmd == nil || !m.loading || m.reloadAfterAdd {
		t.Fatal("refresh lost")
	}
}

func TestProfileFormPasteEditingAndPreviewCannotBeEdited(t *testing.T) {
	m, _ := profileModel(t)
	m = nameProfile(m, "")
	next, _ := m.Update(tea.PasteMsg{Content: "wok\n"})
	m = next.(Model)
	m, _ = profileKey(m, tea.KeyLeft, "")
	m, _ = profileKey(m, 'r', "r")
	if string(m.profileName) != "work" {
		t.Fatal("paste/edit failed")
	}
	m, _ = profileKey(m, tea.KeyEnter, "")
	next, _ = m.Update(tea.PasteMsg{Content: "other"})
	m = next.(Model)
	m, _ = profileKey(m, 'x', "x")
	if string(m.profileName) != "work" {
		t.Fatal("changed confirmed command")
	}
	for _, width := range []int{40, 60, 80} {
		m.width = width
		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatal("form overflow")
			}
		}
	}
}

func TestLoginPreviewScrollsInSmallTerminal(t *testing.T) {
	m, _ := profileModel(t)
	m = nameProfile(m, "personal")
	m, _ = profileKey(m, tea.KeyEnter, "")
	m.width = 40
	m.height = 14
	for i := 0; i < 4; i++ {
		m, _ = profileKey(m, tea.KeyPgDown, "")
	}
	if m.profileScroll == 0 || !strings.Contains(ansi.Strip(m.View().Content), "claude auth login") {
		t.Fatal("login command inaccessible", ansi.Strip(m.View().Content))
	}
	for i := 0; i < 4; i++ {
		m, _ = profileKey(m, tea.KeyPgUp, "")
	}
	if m.profileScroll != 0 {
		t.Fatal("cannot scroll back")
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

func TestAddCodexProfileRequiresConfirmationAndSuccessfulLogin(t *testing.T) {
	m, claudeStore := profileModel(t)
	store := profile.Store{Home: claudeStore.Home, Provider: "codex"}
	codexActions := &ProfileActions{Name: "Codex", Kind: "codex", Directory: store.Directory, Command: codexcli.LoginCommand,
		Login: func(ctx context.Context, name string) tea.ExecCommand {
			return &fakeProfileLogin{run: func() error { _, err := store.Prepare(ctx, name); return err }}
		}, Register: store.Register}
	m = m.WithProfileProviders([]*ProfileActions{m.profileActions, codexActions})
	m = nameProfile(m, "work")
	m, _ = profileKey(m, tea.KeyTab, "")
	if m.profileActions != codexActions {
		t.Fatal("provider selection failed")
	}
	m, cmd := profileKey(m, tea.KeyEnter, "")
	if cmd != nil || !m.confirmProfile {
		t.Fatal("preview started login")
	}
	m.height = 40
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Confirm Codex login") || !strings.Contains(view, "CODEX_HOME=") || !strings.Contains(view, "codex login") {
		t.Fatal("incorrect Codex preview", view)
	}
	entries, _ := os.ReadDir(store.Home)
	if len(entries) != 0 {
		t.Fatal("preview created files")
	}
	m, _ = profileKey(m, tea.KeyTab, "")
	if m.profileActions != codexActions {
		t.Fatal("confirmed provider changed")
	}
	m, cmd = profileKey(m, tea.KeyEnter, "")
	if cmd == nil || m.profileLogin == nil {
		t.Fatal("login not scheduled")
	}
	if err := m.profileLogin.Run(); err != nil {
		t.Fatal(err)
	}
	next, save := m.Update(profileLoginFinishedMsg{})
	m = next.(Model)
	if save == nil {
		t.Fatal("successful login did not schedule save")
	}
	next, _ = m.Update(save())
	m = next.(Model)
	if m.createdDirectory != store.Directory("work") {
		t.Fatal("Codex profile not saved")
	}
	data, _ := os.ReadFile(store.ConfigPath())
	profiles, err := profile.ParseEntries(data)
	if err != nil || profiles[len(profiles)-1].Provider != "codex" {
		t.Fatal(profiles, err)
	}
}
