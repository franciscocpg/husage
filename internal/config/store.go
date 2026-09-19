// Package config persists husage's dashboard preferences.
package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultRefresh = 5 * time.Minute

type Settings struct {
	RefreshInterval string `json:"refresh_interval"`
}

func Default() Settings { return Settings{RefreshInterval: "5m"} }

func (s Settings) Interval() (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s.RefreshInterval))
	if err != nil || d < 5*time.Second {
		return 0, errors.New("Enter an interval of at least 5s, such as 30s, 5m or 1h.")
	}
	return d, nil
}

type Store struct{ Home string }

func (s Store) Path() string { return filepath.Join(s.Home, ".config", "husage", "config.json") }

func (s Store) read() (Settings, map[string]json.RawMessage, bool, error) {
	settings := Default()
	fields := make(map[string]json.RawMessage)
	info, err := os.Lstat(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return settings, fields, false, nil
	}
	if err != nil {
		return settings, nil, true, err
	}
	if !info.Mode().IsRegular() {
		return settings, nil, true, errors.New("configuration is not a regular file; it was left unchanged")
	}
	data, err := os.ReadFile(s.Path())
	if err != nil {
		return settings, nil, true, err
	}
	if err = json.Unmarshal(data, &fields); err != nil || fields == nil {
		return settings, nil, true, errors.New("configuration must be a JSON object; it was left unchanged")
	}
	if raw, ok := fields["refresh_interval"]; ok {
		if err = json.Unmarshal(raw, &settings.RefreshInterval); err != nil || string(raw) == "null" {
			return settings, nil, true, errors.New("refresh_interval must be a duration string")
		}
	}
	_, err = settings.Interval()
	return settings, fields, true, err
}

// Load uses defaults when no file exists, without creating files.
func (s Store) Load() (Settings, error) {
	settings, _, _, err := s.read()
	if err != nil {
		return settings, fmt.Errorf("read %s: %w", s.Path(), err)
	}
	return settings, nil
}

// Ensure creates the default configuration on the first interactive launch.
func (s Store) Ensure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, _, exists, err := s.read()
	if err != nil || exists {
		return err
	}
	return s.write(ctx, Default(), true)
}

func (s Store) Save(ctx context.Context, settings Settings) error {
	return s.write(ctx, settings, false)
}

func (s Store) write(ctx context.Context, settings Settings, onlyIfMissing bool) error {
	if _, err := settings.Interval(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.Path()+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("lock configuration (retry after other saves finish): %w", err)
	}
	defer func() { lock.Close(); os.Remove(s.Path() + ".lock") }()
	_, fields, exists, err := s.read()
	if err != nil {
		return err
	}
	if onlyIfMissing && exists {
		return nil
	}
	fields["refresh_interval"], _ = json.Marshal(strings.TrimSpace(settings.RefreshInterval))
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.Path()), ".config-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.Path())
}
