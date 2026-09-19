package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/franciscocpg/husage/internal/claude"
)

type profileDirs []string

func (p *profileDirs) String() string { return strings.Join(*p, ", ") }
func (p *profileDirs) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("Claude configuration directory cannot be empty; use current for the active profile")
	}
	*p = append(*p, value)
	return nil
}

// Profile lists contain only directory paths; credentials remain in Claude's
// native stores. Explicit CLI directories override a saved profile list.
func claudeProfiles(home string, dirs []string, profilesPath string) (claude.Options, []claude.Options, error) {
	active := claude.Options{Home: home, ConfigDir: expand(os.Getenv("CLAUDE_CONFIG_DIR"), home), SecureDir: expand(os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR"), home)}
	if len(dirs) == 0 {
		path := expand(profilesPath, home)
		explicit := path != ""
		if !explicit {
			path = filepath.Join(home, ".config", "husage", "profiles.json")
		}
		data, err := os.ReadFile(path)
		if err != nil && (explicit || !os.IsNotExist(err)) {
			return active, nil, fmt.Errorf("cannot read profile list %s", path)
		}
		if err == nil {
			if json.Unmarshal(data, &dirs) != nil || len(dirs) == 0 {
				return active, nil, fmt.Errorf("profile list %s must be a nonempty JSON array of Claude configuration directories", path)
			}
		}
	}
	if len(dirs) == 0 {
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
