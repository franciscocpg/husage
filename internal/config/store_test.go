package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultsAndPersistence(t *testing.T) {
	s := Store{Home: t.TempDir()}
	settings, err := s.Load()
	if err != nil || settings != Default() {
		t.Fatal(settings, err)
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Fatal("read created a file")
	}
	if err := s.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.Path())
	if err != nil || json.Unmarshal(data, &settings) != nil || settings != Default() {
		t.Fatal("default file not created", err)
	}
	if err := s.Save(context.Background(), Settings{RefreshInterval: "10m"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	settings, err = (Store{Home: s.Home}).Load()
	interval, _ := settings.Interval()
	if err != nil || interval != 10*time.Minute {
		t.Fatal("saved interval not preserved", settings, err)
	}
	info, err := os.Stat(s.Path())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("configuration permissions", info, err)
	}
	info, err = os.Stat(filepath.Dir(s.Path()))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("directory permissions", info, err)
	}
}

func TestInvalidConfigurationIsPreserved(t *testing.T) {
	for _, data := range []string{`{`, `[]`, `null`, `{"refresh_interval":null}`, `{"refresh_interval":30}`, `{"refresh_interval":"0s"}`, `{"refresh_interval":"4s"}`, `{"refresh_interval":"invalid"}`} {
		t.Run(data, func(t *testing.T) {
			s := Store{Home: t.TempDir()}
			if err := os.MkdirAll(filepath.Dir(s.Path()), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(s.Path(), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(); err == nil {
				t.Fatal("invalid configuration loaded")
			}
			if err := s.Save(context.Background(), Default()); err == nil {
				t.Fatal("invalid configuration overwritten")
			}
			after, err := os.ReadFile(s.Path())
			if err != nil || string(after) != data {
				t.Fatal("original content changed", err)
			}
		})
	}
}

func TestSavePreservesOtherFieldsAndFailuresCanRetry(t *testing.T) {
	s := Store{Home: t.TempDir()}
	ctx := context.Background()
	if err := s.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	original := `{"refresh_interval":"5m","future_setting":{"enabled":true}}`
	if err := os.WriteFile(s.Path(), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path()+".lock", []byte("other writer"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, Settings{RefreshInterval: "1h"}); err == nil {
		t.Fatal("ignored save lock")
	}
	data, _ := os.ReadFile(s.Path())
	if string(data) != original {
		t.Fatal("failed save changed configuration")
	}
	if err := os.Remove(s.Path() + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ctx, Settings{RefreshInterval: "1h"}); err != nil {
		t.Fatal(err)
	}
	_, fields, _, err := s.read()
	var future map[string]bool
	if err != nil || json.Unmarshal(fields["future_setting"], &future) != nil || !future["enabled"] {
		t.Fatal("unknown fields lost", err)
	}
}

func TestInvalidInputCancellationAndSymlink(t *testing.T) {
	s := Store{Home: t.TempDir()}
	if err := s.Save(context.Background(), Settings{RefreshInterval: "-1m"}); err == nil {
		t.Fatal("invalid input accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Ensure(ctx); err == nil {
		t.Fatal("cancelled write accepted")
	}
	entries, _ := os.ReadDir(s.Home)
	if len(entries) != 0 {
		t.Fatal("invalid or cancelled save created files")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(s.Home, "original.json")
	if err := os.WriteFile(target, []byte(`{"refresh_interval":"1h"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, s.Path()); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), Default()); err == nil {
		t.Fatal("accepted symbolic link")
	}
	data, _ := os.ReadFile(target)
	if string(data) != `{"refresh_interval":"1h"}` {
		t.Fatal("changed symlink target")
	}
}
