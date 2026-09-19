package profile

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

// Legacy strings remain Claude profiles; typed entries identify other providers.
type Entry struct {
	Provider  string `json:"provider"`
	Directory string `json:"directory"`
}

func ParseEntries(data []byte) ([]Entry, error) {
	var raw []json.RawMessage
	if json.Unmarshal(data, &raw) != nil || len(raw) == 0 {
		return nil, errors.New("Profile list must be a nonempty JSON array.")
	}
	entries := make([]Entry, 0, len(raw))
	for _, item := range raw {
		entry := Entry{Provider: "claude"}
		if json.Unmarshal(item, &entry.Directory) != nil {
			entry = Entry{}
			if json.Unmarshal(item, &entry) != nil {
				return nil, errors.New("Invalid profile entry.")
			}
		}
		if entry.Provider != "claude" && entry.Provider != "codex" {
			return nil, errors.New("Profile provider must be claude or codex.")
		}
		if entry.Directory != "current" && !filepath.IsAbs(entry.Directory) && !strings.HasPrefix(entry.Directory, "~/") {
			return nil, errors.New("Profile directory must be absolute, start with ~/ or be current.")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
