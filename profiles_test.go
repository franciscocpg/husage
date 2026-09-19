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
