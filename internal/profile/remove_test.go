package profile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveSubscriptionPreservesNativeFilesAndOtherProfiles(t *testing.T) {
	s := Store{Home: t.TempDir(), Provider: "codex"}
	current := filepath.Join(s.Home, ".codex")
	other := filepath.Join(s.Home, "other")
	os.MkdirAll(filepath.Dir(s.ConfigPath()), 0700)
	original := `["current",{"provider":"codex","directory":"current"},{"provider":"codex","directory":"~/.codex"},{"provider":"codex","directory":"~/other","note":"keep"}]`
	os.WriteFile(s.ConfigPath(), []byte(original), 0600)
	os.MkdirAll(current, 0700)
	auth := filepath.Join(current, "auth.json")
	os.WriteFile(auth, []byte("existing-native-login"), 0600)
	if err := s.Remove(context.Background(), "", current, []string{current}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(s.ConfigPath())
	entries, err := ParseEntries(data)
	if err != nil || len(entries) != 2 || entries[0].Provider != "claude" || entries[1].Directory != "~/other" || !strings.Contains(string(data), `"note": "keep"`) {
		t.Fatal(string(data), err)
	}
	native, _ := os.ReadFile(auth)
	if string(native) != "existing-native-login" {
		t.Fatal("native login modified")
	}
	if err := s.Remove(context.Background(), "", current, []string{other}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(s.ConfigPath())
	entries, err = ParseEntries(data)
	if err != nil || len(entries) != 2 || !entries[1].Disabled || entries[1].Provider != "codex" {
		t.Fatal(string(data), err)
	}
	info, _ := os.Stat(s.ConfigPath())
	if info.Mode().Perm() != 0600 {
		t.Fatal("profile list permissions")
	}
	// Adding a new home leaves the removed default disabled.
	dir, err := s.Prepare(context.Background(), "new")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Register(context.Background(), "new"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(s.ConfigPath())
	entries, err = ParseEntries(data)
	if err != nil || len(entries) != 3 || !entries[1].Disabled || entries[2].Directory != dir {
		t.Fatal(string(data), err)
	}
}

func TestRemoveDiscoveredDefaultAndExplicitList(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "cursor"} {
		t.Run(provider, func(t *testing.T) {
			s := Store{Home: t.TempDir(), Provider: provider}
			dir := filepath.Join(s.Home, "."+provider)
			if err := s.Remove(context.Background(), "", dir, []string{dir}); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(s.ConfigPath())
			entries, err := ParseEntries(data)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range entries {
				if e.Provider == provider {
					if !e.Disabled {
						t.Fatal("default retained")
					}
					found = true
				}
			}
			if !found {
				t.Fatal("default will be rediscovered")
			}
		})
	}
	s := Store{Home: t.TempDir()}
	path := filepath.Join(s.Home, "custom.json")
	os.WriteFile(path, []byte(`["current"]`), 0600)
	dir := filepath.Join(s.Home, ".claude")
	if err := s.Remove(context.Background(), path, dir, []string{dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.ConfigPath()); !os.IsNotExist(err) {
		t.Fatal("modified default list for explicit override")
	}
}

func TestRemoveFailurePreservesProfileList(t *testing.T) {
	for _, mode := range []string{"invalid", "locked", "cancelled", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			s := Store{Home: t.TempDir()}
			os.MkdirAll(filepath.Dir(s.ConfigPath()), 0700)
			original := `["current"]`
			if mode == "invalid" {
				original = `{bad`
			}
			os.WriteFile(s.ConfigPath(), []byte(original), 0600)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "cancelled":
				cancel()
			case "locked":
				os.WriteFile(s.ConfigPath()+".lock", []byte("another-save"), 0600)
			case "symlink":
				target := filepath.Join(s.Home, "target.json")
				os.Rename(s.ConfigPath(), target)
				os.Symlink(target, s.ConfigPath())
			}
			dir := filepath.Join(s.Home, ".claude")
			if err := s.Remove(ctx, "", dir, []string{dir}); err == nil {
				t.Fatal("expected removal failure")
			}
			data, _ := os.ReadFile(s.ConfigPath())
			if string(data) != original {
				t.Fatal("failed removal changed list")
			}
		})
	}
}
