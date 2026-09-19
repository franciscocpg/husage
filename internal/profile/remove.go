package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Remove unregisters all directories backing a displayed subscription. Native
// login directories, Keychain items, and usage caches are never modified.
// current is the resolved native configuration directory for this provider.
func (s Store) Remove(ctx context.Context, path, current string, directories []string) error {
	if s.Kind() != "claude" && s.Kind() != "codex" {
		return errors.New("Unsupported profile provider.")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(directories) == 0 || !filepath.IsAbs(current) {
		return errors.New("No subscription directories selected.")
	}
	targets := map[string]bool{}
	for _, dir := range directories {
		if !filepath.IsAbs(dir) {
			return errors.New("Invalid subscription directory.")
		}
		targets[filepath.Clean(dir)] = true
	}
	explicit := path != ""
	if !explicit {
		path = s.ConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("Cannot lock the profile list. Retry after another save finishes.")
	}
	defer func() { lock.Close(); os.Remove(path + ".lock") }()
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("The profile list is a symbolic link; it was left unchanged.")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if explicit || !os.IsNotExist(err) {
			return fmt.Errorf("read profile list: %w", err)
		}
		data = []byte(`["current"]`)
	}
	entries, err := ParseEntries(data)
	if err != nil {
		return err
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	present := false
	for _, e := range entries {
		present = present || e.Provider == s.Kind()
	}
	// A provider absent from legacy lists is discovered automatically.
	if !present && targets[filepath.Clean(current)] {
		e := Entry{Provider: s.Kind(), Directory: "current"}
		b, _ := json.Marshal(e)
		entries, raw = append(entries, e), append(raw, b)
	}
	kept := []json.RawMessage{}
	remaining, removed, disabled := 0, false, false
	for i, e := range entries {
		if e.Provider == s.Kind() {
			if e.Disabled {
				disabled = true
				kept = append(kept, raw[i])
				continue
			}
			dir := e.Directory
			if dir == "current" {
				dir = current
			} else if strings.HasPrefix(dir, "~/") {
				dir = filepath.Join(s.Home, dir[2:])
			}
			if targets[filepath.Clean(dir)] {
				removed = true
				continue
			}
			remaining++
		}
		kept = append(kept, raw[i])
	}
	if !removed {
		return nil
	} // CLI-only profile; the flag remains an explicit override.
	if remaining == 0 && !disabled {
		marker, _ := json.Marshal(Entry{Provider: s.Kind(), Disabled: true})
		kept = append(kept, marker)
	}
	return writePaths(ctx, path, kept)
}
