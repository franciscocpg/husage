package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestClaudeProfilesSeparateCredentialStores(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "active"))
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", filepath.Join(home, "active-secrets"))
	active, profiles, err := claudeProfiles(home, []string{"current", "~/second"}, "")
	if err != nil || len(profiles) != 2 {
		t.Fatal(profiles, err)
	}
	if profiles[0] != active || profiles[1].ConfigDir != filepath.Join(home, "second") || profiles[1].SecureDir != "" {
		t.Fatal("profile credential stores are not isolated", profiles)
	}
	var dirs profileDirs
	if err := dirs.Set("current"); err != nil {
		t.Fatal(err)
	}
	if err := dirs.Set("~/second"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([]string(dirs), []string{"current", "~/second"}) {
		t.Fatal(dirs)
	}
	if dirs.Set("") == nil {
		t.Fatal("accepted empty directory")
	}
}

func TestSavedClaudeProfilesAndCLIOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	path := filepath.Join(home, ".config", "husage", "profiles.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`["current","~/second"]`), 0600); err != nil {
		t.Fatal(err)
	}
	_, profiles, err := claudeProfiles(home, nil, "")
	if err != nil || len(profiles) != 2 || profiles[0].ConfigDir != "" || profiles[1].ConfigDir != filepath.Join(home, "second") {
		t.Fatal(profiles, err)
	}
	_, profiles, err = claudeProfiles(home, []string{"~/explicit"}, "")
	if err != nil || len(profiles) != 1 || profiles[0].ConfigDir != filepath.Join(home, "explicit") {
		t.Fatal(profiles, err)
	}
	_, _, err = claudeProfiles(home, nil, filepath.Join(home, "missing.json"))
	if err == nil {
		t.Fatal("ignored missing explicit config")
	}
	for _, body := range []string{`[]`, `[""]`, `["relative/path"]`, `{`, `null`, `{"accessToken":"never-a-config"}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := claudeProfiles(home, nil, ""); err == nil {
			t.Fatalf("accepted invalid config %s", body)
		}
	}
}

func TestMixedProfilesAndCodexHomeOverride(t *testing.T) {
	home := t.TempDir()
	// The user's requested account selector is scoped to this test process.
	t.Setenv("CODEX_HOME", filepath.Join(home, "active-codex"))
	path := filepath.Join(home, "profiles.json")
	if err := os.WriteFile(path, []byte(`["current","~/claude-work",{"provider":"codex","directory":"current"},{"provider":"codex","directory":"~/codex-work"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	_, claudeEntries, err := claudeProfiles(home, nil, path)
	if err != nil || len(claudeEntries) != 2 || claudeEntries[1].ConfigDir != filepath.Join(home, "claude-work") {
		t.Fatal(claudeEntries, err)
	}
	codexEntries, err := codexProfiles(home, nil, path)
	if err != nil || len(codexEntries) != 2 || !codexEntries[0].Active || codexEntries[1].Active || codexEntries[1].Home != filepath.Join(home, "codex-work") {
		t.Fatal(codexEntries, err)
	}
	codexEntries, err = codexProfiles(home, []string{"~/explicit"}, path)
	if err != nil || len(codexEntries) != 1 || codexEntries[0].Home != filepath.Join(home, "explicit") {
		t.Fatal(codexEntries, err)
	}
	t.Setenv("CODEX_HOME", "")
	codexEntries, err = codexProfiles(home, nil, "")
	if err != nil || len(codexEntries) != 1 || !codexEntries[0].Optional || codexEntries[0].Home != filepath.Join(home, ".codex") {
		t.Fatal(codexEntries, err)
	}
	if _, err := codexProfiles(home, []string{"relative"}, ""); err == nil {
		t.Fatal("accepted relative Codex home")
	}
}

func TestRemovedProvidersDoNotReappearOnRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	path := filepath.Join(home, "profiles.json")
	os.WriteFile(path, []byte(`[{"provider":"claude","disabled":true},{"provider":"codex","disabled":true}]`), 0600)
	_, claudeEntries, err := claudeProfiles(home, nil, path)
	if err != nil || len(claudeEntries) != 0 {
		t.Fatal("Claude rediscovered", claudeEntries, err)
	}
	codexEntries, err := codexProfiles(home, nil, path)
	if err != nil || len(codexEntries) != 0 {
		t.Fatal("Codex rediscovered", codexEntries, err)
	}
	_, claudeEntries, err = claudeProfiles(home, []string{"current"}, path)
	if err != nil || len(claudeEntries) != 1 {
		t.Fatal("explicit Claude flag ignored", err)
	}
	codexEntries, err = codexProfiles(home, []string{"current"}, path)
	if err != nil || len(codexEntries) != 1 {
		t.Fatal("explicit Codex flag ignored", err)
	}
	os.WriteFile(path, []byte(`[{"provider":"claude","disabled":true},{"provider":"claude","directory":"~/new"},{"provider":"codex","disabled":true},{"provider":"codex","directory":"~/new-codex"}]`), 0600)
	_, claudeEntries, err = claudeProfiles(home, nil, path)
	if err != nil || len(claudeEntries) != 1 || claudeEntries[0].ConfigDir != filepath.Join(home, "new") {
		t.Fatal(claudeEntries, err)
	}
	codexEntries, err = codexProfiles(home, nil, path)
	if err != nil || len(codexEntries) != 1 || codexEntries[0].Home != filepath.Join(home, "new-codex") {
		t.Fatal(codexEntries, err)
	}
}

func TestCursorCurrentProfileAndRemovalMarker(t *testing.T) {
	home := t.TempDir()
	opts, err := cursorProfile(home, "")
	if err != nil || !opts.Optional || opts.Disabled {
		t.Fatal(opts, err)
	}
	path := filepath.Join(home, "profiles.json")
	os.WriteFile(path, []byte(`[{"provider":"cursor","directory":"current"}]`), 0600)
	opts, err = cursorProfile(home, path)
	if err != nil || opts.Optional || opts.Disabled {
		t.Fatal(opts, err)
	}
	os.WriteFile(path, []byte(`[{"provider":"cursor","disabled":true}]`), 0600)
	opts, err = cursorProfile(home, path)
	if err != nil || !opts.Disabled {
		t.Fatal(opts, err)
	}
	os.WriteFile(path, []byte(`[{"provider":"cursor","directory":"~/different-login"}]`), 0600)
	if _, err = cursorProfile(home, path); err == nil {
		t.Fatal("pretended settings directories isolate Cursor logins")
	}
}
