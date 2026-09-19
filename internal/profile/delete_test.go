package profile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveManagedProfileAllowsFreshLoginWithSameName(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			s := Store{Home: t.TempDir(), Provider: provider}
			ctx := context.Background()
			dir, err := s.Prepare(ctx, "work")
			if err != nil {
				t.Fatal(err)
			}
			credential := "auth.json"
			if provider == "claude" {
				credential = ".credentials.json"
			}
			if err := os.WriteFile(filepath.Join(dir, credential), []byte("fake-login"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Register(ctx, "work"); err != nil {
				t.Fatal(err)
			}
			// Recursive deletion removes the link, never the files it points at.
			external := t.TempDir()
			sentinel := filepath.Join(external, "keep")
			os.WriteFile(sentinel, []byte("external"), 0600)
			if err := os.Symlink(external, filepath.Join(dir, "sessions")); err != nil {
				t.Fatal(err)
			}
			if err := s.Remove(ctx, "", filepath.Join(s.Home, "."+provider), []string{dir}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatal("profile directory retained", err)
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "external" {
				t.Fatal("symlink target changed", err)
			}
			if _, err := s.Prepare(ctx, "work"); err != nil {
				t.Fatal("cannot reuse removed name", err)
			}
			if _, err := os.Stat(filepath.Join(dir, credential)); !os.IsNotExist(err) {
				t.Fatal("old credentials reused")
			}
			if _, err := s.Register(ctx, "work"); err != nil {
				t.Fatal("cannot register reused name", err)
			}
		})
	}
}

func TestRemovalStagingFailureRestoresAllDirectories(t *testing.T) {
	s := Store{Home: t.TempDir(), Provider: "codex"}
	ctx := context.Background()
	first, _ := s.Prepare(ctx, "a")
	os.WriteFile(filepath.Join(first, "auth.json"), []byte("fake-login"), 0600)
	s.Register(ctx, "a")
	original, _ := os.ReadFile(s.ConfigPath())
	second := s.Directory("z")
	external := t.TempDir()
	os.Symlink(external, second)
	if err := s.Remove(ctx, "", filepath.Join(s.Home, ".codex"), []string{first, second}); err == nil {
		t.Fatal("accepted linked profile directory")
	}
	if data, err := os.ReadFile(filepath.Join(first, "auth.json")); err != nil || string(data) != "fake-login" {
		t.Fatal("first directory not restored", err)
	}
	if data, _ := os.ReadFile(s.ConfigPath()); string(data) != string(original) {
		t.Fatal("failed removal changed list")
	}
}

func TestCancelledSaveRestoresStagedProfile(t *testing.T) {
	s := Store{Home: t.TempDir(), Provider: "codex"}
	dir, _ := s.Prepare(context.Background(), "work")
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("fake-login"), 0600)
	s.Register(context.Background(), "work")
	original, _ := os.ReadFile(s.ConfigPath())
	staged, err := s.stageRemoval(context.Background(), s.ConfigPath(), map[string]bool{dir: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writePaths(ctx, s.ConfigPath(), []json.RawMessage{json.RawMessage(`"current"`)}); err == nil {
		t.Fatal("ignored cancelled save")
	}
	if err := staged.restore(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "auth.json")); err != nil || string(data) != "fake-login" {
		t.Fatal("credentials not restored", err)
	}
	if data, _ := os.ReadFile(s.ConfigPath()); string(data) != string(original) {
		t.Fatal("failed save changed list")
	}
}

func TestRemoveDoesNotTraverseManagedParentSymlink(t *testing.T) {
	s := Store{Home: t.TempDir(), Provider: "codex"}
	os.MkdirAll(filepath.Dir(s.ConfigPath()), 0700)
	external := t.TempDir()
	os.Mkdir(filepath.Join(external, "work"), 0700)
	sentinel := filepath.Join(external, "work", "auth.json")
	os.WriteFile(sentinel, []byte("fake-login"), 0600)
	os.Symlink(external, filepath.Dir(s.Directory("work")))
	entries, _ := json.Marshal([]Entry{{Provider: "codex", Directory: s.Directory("work")}})
	os.WriteFile(s.ConfigPath(), entries, 0600)
	if err := s.Remove(context.Background(), "", filepath.Join(s.Home, ".codex"), []string{s.Directory("work")}); err == nil {
		t.Fatal("followed linked parent")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "fake-login" {
		t.Fatal("external credentials changed", err)
	}
	if data, _ := os.ReadFile(s.ConfigPath()); string(data) != string(entries) {
		t.Fatal("failed removal changed list")
	}
}

func TestRemoveManagedCLIOnlyProfileAndProtectEmbeddedList(t *testing.T) {
	for _, embedded := range []bool{false, true} {
		s := Store{Home: t.TempDir(), Provider: "codex"}
		dir, _ := s.Prepare(context.Background(), "work")
		path := filepath.Join(s.Home, "custom.json")
		if embedded {
			path = filepath.Join(dir, "profiles.json")
		}
		os.WriteFile(path, []byte(`["current"]`), 0600)
		err := s.Remove(context.Background(), path, filepath.Join(s.Home, ".codex"), []string{dir})
		if embedded {
			if err == nil {
				t.Fatal("deleted the selected profile list")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("CLI-only managed directory retained")
			}
		}
		if data, _ := os.ReadFile(path); string(data) != `["current"]` {
			t.Fatal("unrelated list changed")
		}
	}
}
