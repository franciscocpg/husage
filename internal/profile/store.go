// Package profile manages husage's list of native Claude configuration paths.
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Store struct{ Home string }

func (s Store) ConfigPath() string {
	return filepath.Join(s.Home, ".config", "husage", "profiles.json")
}
func (s Store) Directory(name string) string {
	return filepath.Join(s.Home, ".config", "husage", "claude", name)
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,47}$`)

func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return errors.New("Use 1–48 letters, numbers, dashes or underscores; start with a letter or number.")
	}
	return nil
}

// Add is called only after a user submits the form. It creates a fresh native
// config directory and atomically appends its path, without touching credentials.
func (s Store) Add(ctx context.Context, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	config := s.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		return "", fmt.Errorf("create husage configuration directory: %w", err)
	}
	lock, err := os.OpenFile(config+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return "", errors.New("The profile list is locked by another save. Retry after it finishes.")
	}
	if err != nil {
		return "", fmt.Errorf("lock profile list: %w", err)
	}
	defer func() { lock.Close(); os.Remove(config + ".lock") }()
	if info, err := os.Lstat(config); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("The profile list is a symbolic link; it was left unchanged.")
	}
	data, err := os.ReadFile(config)
	paths := []string{"current"}
	if err == nil {
		if json.Unmarshal(data, &paths) != nil || len(paths) == 0 {
			return "", errors.New("The existing profile list is invalid; it was left unchanged.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read profile list: %w", err)
	}
	dir := s.Directory(name)
	for _, p := range paths {
		if p == "current" {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(s.Home, p[2:])
		}
		if !filepath.IsAbs(p) {
			return "", errors.New("The existing profile list contains an invalid directory; it was left unchanged.")
		}
		if filepath.Clean(p) == dir {
			return "", errors.New("A profile with this directory is already registered. Choose another name.")
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return "", fmt.Errorf("create profile parent directory: %w", err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", errors.New("That directory already exists. Choose another profile name.")
		}
		return "", fmt.Errorf("create profile directory: %w", err)
	}
	saved := false
	defer func() {
		if !saved {
			os.Remove(dir)
		}
	}() // Only our new, still-empty directory.
	paths = append(paths, dir)
	data, err = json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(config), ".profiles-*.json")
	if err != nil {
		return "", fmt.Errorf("prepare profile list: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return "", fmt.Errorf("write profile list: %w", err)
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return "", fmt.Errorf("save profile list: %w", err)
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), config); err != nil {
		return "", fmt.Errorf("replace profile list: %w", err)
	}
	saved = true
	return dir, nil
}
