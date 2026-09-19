package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/franciscocpg/husage/internal/claude"
	"github.com/franciscocpg/husage/internal/codex"
	"github.com/franciscocpg/husage/internal/cursor"
	"github.com/franciscocpg/husage/internal/profile"
)

type profileDirs []string

func (p *profileDirs) String() string { return strings.Join(*p, ", ") }
func (p *profileDirs) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("Profile directory cannot be empty; use current for the active profile")
	}
	*p = append(*p, value)
	return nil
}

func cursorProfile(home, profilesPath string) (cursor.Options, error) {
	dirs, err := savedDirectories(home, profilesPath, "cursor")
	if err != nil {
		return cursor.Options{}, err
	}
	opts := cursor.Options{Home: home, Optional: dirs == nil, Disabled: dirs != nil && len(dirs) == 0}
	for _, dir := range dirs {
		if dir != "current" && filepath.Clean(expand(dir, home)) != filepath.Clean(cursor.NativeDirectory(home)) {
			return opts, fmt.Errorf("Cursor currently supports the existing native CLI login; use directory current")
		}
	}
	return opts, nil
}

// Profile lists contain only directory paths; credentials remain in Claude's
// native stores. Explicit CLI directories override a saved profile list.
func claudeProfiles(home string, dirs []string, profilesPath string) (claude.Options, []claude.Options, error) {
	active := claude.Options{Home: home, ConfigDir: expand(os.Getenv("CLAUDE_CONFIG_DIR"), home), SecureDir: expand(os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR"), home)}
	if len(dirs) == 0 {
		var err error
		dirs, err = savedDirectories(home, profilesPath, "claude")
		if err != nil {
			return active, nil, err
		}
	}
	if dirs == nil {
		dirs = []string{"current"}
	}
	options := make([]claude.Options, 0, len(dirs))
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			return active, nil, fmt.Errorf("empty Claude profile directory; use current for the active profile")
		}
		if dir == "current" {
			options = append(options, active)
			continue
		}
		dir = expand(dir, home)
		if !filepath.IsAbs(dir) {
			return active, nil, fmt.Errorf("Claude profile directory must be absolute or start with ~/: %s", dir)
		}
		// Do not apply one process-wide secure-store override to other accounts.
		options = append(options, claude.Options{Home: home, ConfigDir: dir})
	}
	return active, options, nil
}

func savedDirectories(home, path, provider string) ([]string, error) {
	explicit := path != ""
	path = expand(path, home)
	if !explicit {
		path = filepath.Join(home, ".config", "husage", "profiles.json")
	}
	data, err := os.ReadFile(path)
	if !explicit && os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read profile list %s", path)
	}
	entries, err := profile.ParseEntries(data)
	if err != nil {
		return nil, fmt.Errorf("invalid profile list %s: %w", path, err)
	}
	var dirs []string
	for _, entry := range entries {
		if entry.Provider == provider {
			if entry.Disabled {
				if dirs == nil {
					dirs = []string{}
				}
				continue
			}
			dirs = append(dirs, entry.Directory)
		}
	}
	return dirs, nil
}

func codexProfiles(home string, dirs []string, profilesPath string) ([]codex.Options, error) {
	active := expand(os.Getenv("CODEX_HOME"), home)
	if active == "" {
		active = filepath.Join(home, ".codex")
	}
	if !filepath.IsAbs(active) {
		return nil, fmt.Errorf("CODEX_HOME must be an absolute directory")
	}
	if len(dirs) == 0 {
		var err error
		dirs, err = savedDirectories(home, profilesPath, "codex")
		if err != nil {
			return nil, err
		}
	}
	optional := dirs == nil
	if optional {
		dirs = []string{"current"}
	}
	var options []codex.Options
	for _, dir := range dirs {
		if dir == "current" {
			dir = active
		} else {
			dir = expand(dir, home)
		}
		if !filepath.IsAbs(dir) {
			return nil, fmt.Errorf("Codex home must be absolute, start with ~/ or be current")
		}
		dir = filepath.Clean(dir)
		options = append(options, codex.Options{Home: dir, Active: dir == filepath.Clean(active), Optional: optional})
	}
	return options, nil
}
