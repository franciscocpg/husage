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

// Prepare creates or reuses an unfinished login directory after confirmation.
// The profile list remains unchanged until Register is called after login succeeds.
func (s Store) Prepare(ctx context.Context, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := s.Directory(name)
	if _, err := s.readPaths(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return "", fmt.Errorf("create profile parent directory: %w", err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			if err := checkUnfinishedDirectory(dir); err != nil {
				return "", err
			}
			return dir, nil
		}
		return "", fmt.Errorf("create profile directory: %w", err)
	}
	return dir, nil
}

// Claude creates configuration and cache files before authentication finishes.
// Those files alone must not prevent a fresh attempt after closing the form or
// restarting husage. Preserve directories with credentials or account metadata;
// on macOS the credentials themselves may be stored outside the directory.
func checkUnfinishedDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return errors.New("That profile path is not a regular directory. Choose another profile name.")
	}
	if _, err := os.Lstat(filepath.Join(dir, ".credentials.json")); err == nil {
		return errors.New("That directory already contains credentials; it was left unchanged. Choose another profile name.")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check existing profile credentials: %w", err)
	}
	config := filepath.Join(dir, ".claude.json")
	info, err = os.Lstat(config)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check existing profile metadata: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("Existing profile metadata is not a regular file; it was left unchanged.")
	}
	data, err := os.ReadFile(config)
	if err != nil {
		return fmt.Errorf("read existing profile metadata: %w", err)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(data, &metadata); err != nil || metadata == nil {
		return errors.New("Existing profile metadata is invalid; it was left unchanged.")
	}
	if account := metadata["oauthAccount"]; len(account) != 0 && string(account) != "null" {
		return errors.New("That directory already contains account metadata; it was left unchanged. Choose another profile name.")
	}
	return nil
}

func (s Store) readPaths(dir string) ([]string, error) {
	config := s.ConfigPath()
	if info, err := os.Lstat(config); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("The profile list is a symbolic link; it was left unchanged.")
	}
	data, err := os.ReadFile(config)
	paths := []string{"current"}
	if err == nil {
		if json.Unmarshal(data, &paths) != nil || len(paths) == 0 {
			return nil, errors.New("The existing profile list is invalid; it was left unchanged.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read profile list: %w", err)
	}
	for _, p := range paths {
		if p == "current" {
			continue
		}
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(s.Home, p[2:])
		}
		if !filepath.IsAbs(p) {
			return nil, errors.New("The existing profile list contains an invalid directory; it was left unchanged.")
		}
		if filepath.Clean(p) == dir {
			return nil, errors.New("A profile with this directory is already registered. Choose another name.")
		}
	}
	return paths, nil
}

// Register atomically appends a prepared profile after successful authentication.
// It rereads the list under a short lock, preserving profiles added during login.
func (s Store) Register(ctx context.Context, name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := s.Directory(name)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return "", errors.New("The login directory is missing or no longer a directory.")
	}
	config := s.ConfigPath()
	lock, err := os.OpenFile(config+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return "", errors.New("The profile list is locked by another save. Retry after it finishes.")
	}
	if err != nil {
		return "", fmt.Errorf("lock profile list: %w", err)
	}
	defer func() { lock.Close(); os.Remove(config + ".lock") }()
	paths, err := s.readPaths(dir)
	if err != nil {
		return "", err
	}
	paths = append(paths, dir)
	data, err := json.MarshalIndent(paths, "", "  ")
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
	return dir, nil
}
