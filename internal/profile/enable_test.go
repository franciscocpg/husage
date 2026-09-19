package profile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnableCurrentCursorPreservesOtherProfilesAndIsIdempotent(t *testing.T) {
	s := Store{Home: t.TempDir(), Provider: "cursor"}
	os.MkdirAll(filepath.Dir(s.ConfigPath()), 0700)
	os.WriteFile(s.ConfigPath(), []byte(`["current",{"provider":"codex","directory":"~/work","note":"keep"},{"provider":"cursor","disabled":true}]`), 0600)
	for i := 0; i < 2; i++ {
		if err := s.EnableCurrent(context.Background(), ""); err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(s.ConfigPath())
		entries, err := ParseEntries(data)
		if err != nil || len(entries) != 3 || entries[0].Provider != "claude" || entries[1].Provider != "codex" || entries[2].Directory != "current" || entries[2].Disabled || !strings.Contains(string(data), `"note": "keep"`) {
			t.Fatal(string(data), err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Home, ".config", "husage", "cursor")); !os.IsNotExist(err) {
		t.Fatal("created a new native login directory")
	}
	info, _ := os.Stat(s.ConfigPath())
	if info.Mode().Perm() != 0600 {
		t.Fatal("profile file is not private")
	}
}

func TestEnableCurrentCursorSelectedListAndFailures(t *testing.T) {
	for _, mode := range []string{"default-missing", "custom", "locked", "malformed", "cancelled", "symlink", "custom-missing"} {
		t.Run(mode, func(t *testing.T) {
			s := Store{Home: t.TempDir(), Provider: "cursor"}
			path := s.ConfigPath()
			arg := ""
			if mode == "custom" || mode == "custom-missing" {
				path = filepath.Join(s.Home, "custom.json")
				arg = path
			}
			os.MkdirAll(filepath.Dir(path), 0700)
			original := `[{"provider":"cursor","disabled":true}]`
			if mode == "malformed" {
				original = `{bad`
			}
			if mode != "default-missing" && mode != "custom-missing" {
				os.WriteFile(path, []byte(original), 0600)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "locked":
				os.WriteFile(path+".lock", []byte("another writer"), 0600)
			case "cancelled":
				cancel()
			case "symlink":
				target := filepath.Join(s.Home, "target.json")
				os.Rename(path, target)
				os.Symlink(target, path)
			}
			err := s.EnableCurrent(ctx, arg)
			if mode == "default-missing" || mode == "custom" {
				if err != nil {
					t.Fatal(err)
				}
				data, _ := os.ReadFile(path)
				entries, e := ParseEntries(data)
				if e != nil || entries[len(entries)-1].Disabled || entries[len(entries)-1].Directory != "current" {
					t.Fatal(string(data), e)
				}
				if mode == "custom" {
					if _, err := os.Stat(s.ConfigPath()); !os.IsNotExist(err) {
						t.Fatal("wrote wrong profile list")
					}
				}
			} else {
				if err == nil {
					t.Fatal("expected failure")
				}
				if mode != "custom-missing" {
					data, _ := os.ReadFile(path)
					if string(data) != original {
						t.Fatal("failed save changed list")
					}
				}
			}
		})
	}
}
