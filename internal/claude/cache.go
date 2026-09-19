package claude

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

// Store only usage, keyed by account and organization. Never persist credentials,
// response bodies, login errors, names, or email addresses in this cache.
type cachedUsage struct {
	Version   int                   `json:"version"`
	Key       string                `json:"key"`
	UpdatedAt time.Time             `json:"updated_at"`
	Windows   []subscription.Window `json:"windows"`
}

func (p *Provider) cachePath(id identity) (string, string) {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(id.key())))
	if p.opts.Home == "" {
		return "", key
	}
	return filepath.Join(p.opts.Home, ".cache", "husage", "claude", key+".json"), key
}

func (p *Provider) restoreUsage(id identity, a *subscription.Account) {
	path, key := p.cachePath(id)
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	var cached cachedUsage
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&cached) != nil || cached.Version != 1 || cached.Key != key || cached.UpdatedAt.IsZero() || cached.UpdatedAt.After(p.now()) || len(cached.Windows) == 0 {
		return
	}
	for _, w := range cached.Windows {
		if w.Label == "" || math.IsNaN(w.Used) || math.IsInf(w.Used, 0) || w.Used < 0 || w.Used > 100 {
			return
		}
	}
	a.Windows, a.UpdatedAt = cached.Windows, cached.UpdatedAt
}

func (p *Provider) saveUsage(id identity, a subscription.Account) error {
	path, key := p.cachePath(id)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// Rename a private temporary file so interrupted writes retain the last
	// complete response and readers never observe partial JSON.
	f, err := os.CreateTemp(filepath.Dir(path), ".usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(cachedUsage{Version: 1, Key: key, UpdatedAt: a.UpdatedAt, Windows: a.Windows}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
