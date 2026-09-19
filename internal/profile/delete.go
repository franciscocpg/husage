package profile

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type stagedDirectory struct{ name, temporary string }
type stagedRemoval struct {
	root        *os.Root
	directories []stagedDirectory
}

// Only direct named children of husage's Claude/Codex directory are owned by
// husage. Default native homes, custom paths, and Cursor's shared login are kept.
func (s Store) stageRemoval(ctx context.Context, config string, targets map[string]bool) (*stagedRemoval, error) {
	staged := &stagedRemoval{}
	if s.Kind() != "claude" && s.Kind() != "codex" {
		return staged, nil
	}
	parent := filepath.Dir(s.Directory("placeholder"))
	var names []string
	for dir := range targets {
		if filepath.Dir(dir) != parent || ValidateName(filepath.Base(dir)) != nil {
			continue
		}
		if config == dir || strings.HasPrefix(config, dir+string(filepath.Separator)) {
			return nil, errors.New("The profile list is inside the profile directory; move it before removing this profile.")
		}
		names = append(names, filepath.Base(dir))
	}
	if len(names) == 0 {
		return staged, nil
	}
	sort.Strings(names)
	root, err := os.OpenRoot(s.Home)
	if err != nil {
		return nil, err
	}
	// Open each parent without traversing symlinks, then anchor all mutations to
	// the resulting directory handle. RemoveAll never follows child symlinks.
	for _, part := range []string{".config", "husage", s.Kind()} {
		info, err := root.Lstat(part)
		if errors.Is(err, os.ErrNotExist) {
			root.Close()
			return staged, nil
		}
		if err != nil || !info.IsDir() {
			root.Close()
			return nil, errors.New("The managed profile parent is not a regular directory; it was left unchanged.")
		}
		next, err := root.OpenRoot(part)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	staged.root = root
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, staged.restore())
		}
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() {
			return nil, errors.Join(errors.New("The profile path is not a regular directory; it was left unchanged."), staged.restore())
		}
		temporary := ".removing-" + name + "-" + rand.Text()
		if err := root.Rename(name, temporary); err != nil {
			return nil, errors.Join(fmt.Errorf("prepare profile directory removal: %w", err), staged.restore())
		}
		staged.directories = append(staged.directories, stagedDirectory{name, temporary})
	}
	return staged, nil
}

func (s *stagedRemoval) restore() error {
	if s.root == nil {
		return nil
	}
	defer s.root.Close()
	var result error
	for i := len(s.directories) - 1; i >= 0; i-- {
		d := s.directories[i]
		if _, err := s.root.Lstat(d.name); !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("cannot restore %s; files remain in %s", d.name, filepath.Join(s.root.Name(), d.temporary)))
			continue
		}
		result = errors.Join(result, s.root.Rename(d.temporary, d.name))
	}
	return result
}

func (s *stagedRemoval) delete() error {
	if s.root == nil {
		return nil
	}
	defer s.root.Close()
	var result error
	for _, d := range s.directories {
		if err := s.root.RemoveAll(d.temporary); err != nil {
			// Keep any remaining files at their original path when possible so retrying
			// removal can finish cleanup. The profile list has already been saved.
			remaining := filepath.Join(s.root.Name(), d.temporary)
			if _, statErr := s.root.Lstat(d.name); errors.Is(statErr, os.ErrNotExist) {
				if renameErr := s.root.Rename(d.temporary, d.name); renameErr == nil {
					remaining = filepath.Join(s.root.Name(), d.name)
				}
			}
			result = errors.Join(result, fmt.Errorf("profile list updated, but directory cleanup failed at %s: %w", remaining, err))
		}
	}
	return result
}
