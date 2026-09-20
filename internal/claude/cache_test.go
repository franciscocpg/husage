package claude

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPersistentUsageSurvivesRestartAndFailures(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	id := identity{AccountID: "user", OrgID: "work"}
	status := 200
	body := `{"five_hour":{"utilization":38}}`
	calls := 0
	newProvider := func() *Provider {
		p := New(Options{Home: home})
		p.now = func() time.Time { return now }
		p.readToken = func(context.Context) (string, error) { return "private-test-token", nil }
		p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			if status == 0 {
				return nil, errors.New("connection failed")
			}
			r := response(status, body)
			if status == 429 {
				r.Header.Set("Retry-After", "900")
			}
			return r, nil
		})
		return p
	}
	p := newProvider()
	first := p.loadCurrent(context.Background(), id)[0]
	if first.Error != "" || first.Stale || len(first.Windows) != 1 {
		t.Fatal(first)
	}
	path, _ := p.cachePath(id)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	dir, _ := os.Stat(filepath.Dir(path))
	if info.Mode().Perm() != 0600 || dir.Mode().Perm() != 0700 || strings.Contains(string(original), "private-test-token") {
		t.Fatal("unsafe cache")
	}
	now = now.Add(time.Minute)
	for _, failure := range []int{429, 500, 0, 200} {
		status = failure
		body = `invalid-json`
		p = newProvider() // A new process has no in-memory usage.
		a := p.loadCurrent(context.Background(), id)[0]
		if a.Error == "" || !a.Stale || len(a.Windows) != 1 || a.Windows[0].Used != 38 || !a.UpdatedAt.Equal(first.UpdatedAt) {
			t.Fatalf("failure %d lost last successful response: %+v", failure, a)
		}
		if a.Warning != "" {
			t.Fatalf("temporary failure %d incorrectly requested authentication: %s", failure, a.Warning)
		}
		saved, _ := os.ReadFile(path)
		if string(saved) != string(original) {
			t.Fatal("failure overwrote successful cache")
		}
		before := calls
		p.loadCurrent(context.Background(), id)
		if calls != before {
			t.Fatal("cached fallback bypassed cooldown")
		}
		if failure == 429 && !p.nextFetch.Equal(now.Add(15*time.Minute)) {
			t.Fatal("retry-after ignored")
		}
	}
	// A different organization must never inherit this subscription's usage.
	other := p.loadCurrent(context.Background(), identity{AccountID: "user", OrgID: "personal"})[0]
	if len(other.Windows) != 0 || other.Stale {
		t.Fatal("cross-account cache leak")
	}
	status, body = 200, `{"five_hour":{"utilization":42}}`
	now = now.Add(20 * time.Minute)
	recovered := p.loadCurrent(context.Background(), id)[0]
	if recovered.Error != "" || recovered.Stale || recovered.Windows[0].Used != 42 || !recovered.UpdatedAt.Equal(now) {
		t.Fatal(recovered)
	}
	// The recovered response is also persisted for the next restart.
	status = 500
	a := newProvider().loadCurrent(context.Background(), id)[0]
	if !a.Stale || a.Windows[0].Used != 42 {
		t.Fatal(a)
	}
}

func TestCorruptOrUnavailableCacheDoesNotHideUsage(t *testing.T) {
	p := New(Options{Home: t.TempDir()})
	id := identity{AccountID: "user", OrgID: "org"}
	p.readToken = func(context.Context) (string, error) { return "token", nil }
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(500, ""), nil })
	path, _ := p.cachePath(id)
	write(t, path, `{broken`)
	a := p.loadCurrent(context.Background(), id)[0]
	if len(a.Windows) != 0 || a.Stale || a.Error == "" {
		t.Fatal(a)
	}
	// A blocked cache path reports a save warning without losing fresh usage.
	p = New(Options{Home: t.TempDir()})
	write(t, filepath.Join(p.opts.Home, ".cache"), "blocked")
	p.readToken = func(context.Context) (string, error) { return "token", nil }
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"five_hour":{"utilization":20}}`), nil
	})
	a = p.loadCurrent(context.Background(), id)[0]
	if len(a.Windows) != 1 || a.Windows[0].Used != 20 || a.Stale || !strings.Contains(a.Error, "cache could not be saved") {
		t.Fatal(a)
	}
}
