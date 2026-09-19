package claude

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestUsageAPIAndScopedModelLimits(t *testing.T) {
	p := New(Options{})
	p.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.String() != usageURL || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("anthropic-beta") == "" {
			t.Fatal("incorrect usage request")
		}
		return response(200, `{"five_hour":{"utilization":38,"resets_at":"2026-09-19T03:20:00Z"},"seven_day":{"utilization":48},"seven_day_sonnet":null,"limits":[{"kind":"weekly_scoped","percent":63,"resets_at":"2026-09-23T21:00:00Z","scope":{"model":{"display_name":"Fable"}}}]}`), nil
	})
	w, err := p.fetch(context.Background(), "test-token")
	if err != nil || len(w) != 3 {
		t.Fatalf("windows=%+v err=%v", w, err)
	}
	if w[0].Used != 38 || w[1].Used != 48 || w[2].Used != 63 || w[2].Label != "Current week (Fable)" {
		t.Fatalf("wrong windows %+v", w)
	}
}

func TestUsageFailuresDoNotLeakResponseBodies(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 302} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			p := New(Options{})
			p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(status, "secret-server-response"), nil })
			_, err := p.fetch(context.Background(), "private-token")
			if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-token") {
				t.Fatalf("unsafe error %v", err)
			}
		})
	}
	for _, body := range []string{`{`, `{}`, `{"five_hour":{}}`, `{"five_hour":{"utilization":-1}}`, `{"five_hour":{"utilization":101}}`, `{"five_hour":{"utilization":25,"resets_at":"bad"}}`} {
		p := New(Options{})
		p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		if _, err := p.fetch(context.Background(), "x"); err == nil {
			t.Errorf("accepted invalid payload %s", body)
		}
	}
}

func TestCooldownPreservesUsageAndAccountIsolation(t *testing.T) {
	p := New(Options{})
	now := time.Date(2026, 9, 19, 3, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	p.readToken = func(context.Context) (string, error) { return "test-token", nil }
	calls := 0
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(200, `{"five_hour":{"utilization":38}}`), nil
		}
		r := response(429, "")
		r.Header.Set("Retry-After", "900")
		return r, nil
	})
	id := identity{AccountID: "user", OrgID: "work"}
	a := p.loadCurrent(context.Background(), id)
	if len(a[0].Windows) != 1 {
		t.Fatal(a)
	}
	p.loadCurrent(context.Background(), id)
	if calls != 1 {
		t.Fatal("refetched during cooldown")
	}
	now = now.Add(5 * time.Minute)
	a = p.loadCurrent(context.Background(), id)
	if a[0].Error == "" || a[0].Windows[0].Used != 38 || !p.nextFetch.Equal(now.Add(15*time.Minute)) {
		t.Fatal("failed to retain data/back off")
	}
	now = now.Add(6 * time.Minute)
	p.loadCurrent(context.Background(), id)
	if calls != 2 {
		t.Fatal("ignored retry-after")
	}
	a = p.loadCurrent(context.Background(), identity{AccountID: "user", OrgID: "personal"})
	if calls != 3 || len(a[0].Windows) != 0 {
		t.Fatal("another account inherited usage")
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadNativeClaudeAccount(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(fmt.Sprintf("custom=%t", custom), func(t *testing.T) {
			home := t.TempDir()
			opts := Options{Home: home}
			path := filepath.Join(home, ".claude.json")
			if custom {
				opts.ConfigDir = filepath.Join(home, "work-config")
				path = filepath.Join(opts.ConfigDir, ".claude.json")
				write(t, filepath.Join(home, ".claude.json"), `{"oauthAccount":{"accountUuid":"other-user","organizationUuid":"other-org"}}`)
			}
			write(t, path, `{"oauthAccount":{"accountUuid":"user","organizationUuid":"org","organizationName":"Example Team","emailAddress":"you@example.com"}}`)
			p := New(opts)
			reads, calls := 0, 0
			p.readToken = func(context.Context) (string, error) { reads++; return "test-token", nil }
			p.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Fatal("missing native login")
				}
				return response(200, `{"five_hour":{"utilization":0},"seven_day":{"utilization":48}}`), nil
			})
			a, err := p.Load(context.Background())
			if err != nil || len(a) != 1 {
				t.Fatalf("accounts=%+v error=%v", a, err)
			}
			if a[0].ID != "user:org" || a[0].Name != "Example Team" || a[0].Email != "you@example.com" || !a[0].Active || a[0].Source != "Claude API" || a[0].Error != "" {
				t.Fatal(a)
			}
			if len(a[0].Windows) != 2 || a[0].Windows[0].Used != 0 || a[0].Windows[1].Used != 48 || reads != 1 || calls != 1 {
				t.Fatal("native usage not loaded")
			}
		})
	}
}

func TestMissingAndMalformedClaudeAccount(t *testing.T) {
	home := t.TempDir()
	p := New(Options{Home: home})
	p.readToken = func(context.Context) (string, error) {
		t.Fatal("credentials read without account identity")
		return "", nil
	}
	a, err := p.Load(context.Background())
	if err != nil || len(a) != 0 {
		t.Fatal(a, err)
	}
	path := filepath.Join(home, ".claude.json")
	for _, body := range []string{`{}`, `{"oauthAccount":{"accountUuid":"user"}}`} {
		write(t, path, body)
		a, err = p.Load(context.Background())
		if err != nil || len(a) != 0 {
			t.Fatal(a, err)
		}
	}
	write(t, path, `{`)
	if _, err = p.Load(context.Background()); err == nil {
		t.Fatal("accepted malformed account metadata")
	}
}

func TestNativeLoginFailureKeepsAccountVisible(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".claude.json"), `{"oauthAccount":{"accountUuid":"user","organizationUuid":"org"}}`)
	p := New(Options{Home: home})
	p.readToken = func(context.Context) (string, error) { return "", fmt.Errorf("login required") }
	a, err := p.Load(context.Background())
	if err != nil || len(a) != 1 || !a[0].Active || a[0].Error != "login required" || len(a[0].Windows) != 0 {
		t.Fatal(a, err)
	}
}
