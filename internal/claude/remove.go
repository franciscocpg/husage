package claude

import (
	"errors"
	"path/filepath"
)

// Remove serializes with refresh and persists before changing the live list.
func (g *Profiles) Remove(id string, save func([]string) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	removed := map[*Provider]bool{}
	var dirs []string
	for _, p := range g.providers {
		a, ok := g.last[p]
		if !ok && len(p.cached) > 0 {
			a = p.cached[0]
			ok = true
		}
		if (!ok || a.ID != id) && "profile:"+p.opts.identityPath() != id {
			continue
		}
		removed[p] = true
		dir := p.opts.ConfigDir
		if dir == "" {
			dir = filepath.Join(p.opts.Home, ".claude")
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		return errors.New("This subscription changed. Close this screen and select it again.")
	}
	if err := save(dirs); err != nil {
		return err
	}
	kept := g.providers[:0]
	for _, p := range g.providers {
		if removed[p] {
			delete(g.last, p)
		} else {
			kept = append(kept, p)
		}
	}
	g.providers = kept
	return nil
}
