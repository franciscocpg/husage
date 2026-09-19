package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// EnableCurrent restores the existing Cursor login in the selected profile list.
// No native login directory or credential is created or modified.
func (s Store) EnableCurrent(ctx context.Context, path string) error {
	if s.Kind() != "cursor" {
		return errors.New("Restoring the current login is only supported for Cursor.")
	}
	if err := ctx.Err(); err != nil {
		return err
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
	kept := make([]json.RawMessage, 0, len(raw)+1)
	found := false
	for i, e := range entries {
		if e.Provider == s.Kind() {
			if e.Disabled {
				continue
			}
			if e.Directory == "current" {
				if found {
					continue
				}
				found = true
			}
		}
		kept = append(kept, raw[i])
	}
	if !found {
		entry, _ := json.Marshal(Entry{Provider: s.Kind(), Directory: "current"})
		kept = append(kept, entry)
	}
	return writePaths(ctx, path, kept)
}
