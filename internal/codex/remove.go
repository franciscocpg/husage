package codex

import "errors"

// Remove serializes with refresh and removes all homes deduplicated into a card.
func (g *Profiles) Remove(id string, save func([]string) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	removed := map[*Provider]bool{}
	var homes []string
	for _, p := range g.providers {
		if len(p.cached) > 0 && p.cached[0].ID == id {
			removed[p] = true
			homes = append(homes, p.opts.Home)
		}
	}
	if len(homes) == 0 {
		return errors.New("This subscription changed. Close this screen and select it again.")
	}
	if err := save(homes); err != nil {
		return err
	}
	kept := g.providers[:0]
	for _, p := range g.providers {
		if !removed[p] {
			kept = append(kept, p)
		}
	}
	g.providers = kept
	return nil
}
