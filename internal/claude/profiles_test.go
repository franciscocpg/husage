package claude

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfilesCombineOrganizationsAndIsolateCooldowns(t *testing.T) {
	home := t.TempDir()
	active := Options{Home: home}
	secondary := Options{Home: home, ConfigDir: filepath.Join(home, "secondary")}
	write(t, active.identityPath(), `{"oauthAccount":{"accountUuid":"same-user","organizationUuid":"work","organizationName":"Work","emailAddress":"same@example.com"}}`)
	write(t, secondary.identityPath(), `{"oauthAccount":{"accountUuid":"same-user","organizationUuid":"personal","organizationName":"Personal","emailAddress":"same@example.com"}}`)
	// Active card must sort first regardless of configured order.
	g := NewProfiles(active, []Options{secondary, active})
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	calls := []int{0, 0}
	for i, p := range g.providers {
		p.now = func() time.Time { return now }
		token := fmt.Sprintf("token-%d", i)
		p.readToken = func(context.Context) (string, error) { return token, nil }
		p.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			calls[i]++
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Fatal("credential crossed profiles")
			}
			if i == 0 && calls[i] > 1 {
				r := response(429, "")
				r.Header.Set("Retry-After", "900")
				return r, nil
			}
			return response(200, fmt.Sprintf(`{"five_hour":{"utilization":%d}}`, 20+i*30)), nil
		})
	}
	a, err := g.Load(context.Background())
	if err != nil || len(a) != 2 || a[0].Name != "Work" || !a[0].Active || a[1].Active || a[0].Windows[0].Used != 50 || a[1].Windows[0].Used != 20 {
		t.Fatal(a, err)
	}
	now = now.Add(5 * time.Minute)
	a, err = g.Load(context.Background())
	if err != nil || a[0].Error != "" || a[1].Error == "" || a[1].Windows[0].Used != 20 {
		t.Fatal("one account failure hid other account", a, err)
	}
	now = now.Add(5 * time.Minute)
	_, err = g.Load(context.Background())
	if err != nil || calls[0] != 2 || calls[1] != 3 {
		t.Fatal("backoff crossed profiles", calls, err)
	}
	write(t, secondary.identityPath(), `{`)
	a, err = g.Load(context.Background())
	if err != nil || len(a) != 2 || a[1].Error == "" || len(a[1].Windows) != 1 {
		t.Fatal("metadata failure discarded usage", a, err)
	}
}

func TestProfilesMissingLoginDoesNotHideHealthyAccount(t *testing.T) {
	home := t.TempDir()
	active := Options{Home: home}
	other := Options{Home: home, ConfigDir: filepath.Join(home, "other")}
	write(t, active.identityPath(), `{"oauthAccount":{"accountUuid":"user","organizationUuid":"work"}}`)
	g := NewProfiles(active, []Options{active, other, active})
	if len(g.providers) != 2 {
		t.Fatal("duplicate directory was not collapsed")
	}
	g.providers[0].readToken = func(context.Context) (string, error) { return "x", nil }
	g.providers[0].client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"five_hour":{"utilization":38}}`), nil
	})
	g.providers[1].readToken = func(context.Context) (string, error) {
		t.Fatal("missing profile attempted credentials")
		return "", nil
	}
	a, err := g.Load(context.Background())
	if err != nil || len(a) != 2 || len(a[0].Windows) != 1 || !strings.Contains(a[1].Error, "No Claude login") || a[1].Active {
		t.Fatal(a, err)
	}
}

func TestProfilesDeduplicateSubscriptionAndPreferHealthyLogin(t *testing.T) {
	home := t.TempDir()
	active := Options{Home: home}
	other := Options{Home: home, ConfigDir: filepath.Join(home, "other")}
	for _, opts := range []Options{active, other} {
		write(t, opts.identityPath(), `{"oauthAccount":{"accountUuid":"user","organizationUuid":"same-org"}}`)
	}
	g := NewProfiles(active, []Options{active, other})
	g.providers[0].readToken = func(context.Context) (string, error) { return "", fmt.Errorf("expired login") }
	g.providers[1].readToken = func(context.Context) (string, error) { return "x", nil }
	g.providers[1].client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"five_hour":{"utilization":38}}`), nil
	})
	a, err := g.Load(context.Background())
	if err != nil || len(a) != 1 || a[0].Error != "" || !a[0].Active || a[0].Windows[0].Used != 38 {
		t.Fatal(a, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Load(ctx); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestProfileLogoutClearsPreviouslyLoadedAccount(t *testing.T) {
	home := t.TempDir()
	active := Options{Home: home}
	other := Options{Home: home, ConfigDir: filepath.Join(home, "other")}
	identityJSON := `{"oauthAccount":{"accountUuid":"user","organizationUuid":"work"}}`
	write(t, active.identityPath(), identityJSON)
	g := NewProfiles(active, []Options{active, other})
	p := g.providers[0]
	p.readToken = func(context.Context) (string, error) { return "x", nil }
	calls := 0
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"five_hour":{"utilization":38}}`), nil
	})
	if _, err := g.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	write(t, active.identityPath(), `{}`)
	a, err := g.Load(context.Background())
	if err != nil || len(a) != 2 || a[0].ID == "user:work" || len(a[0].Windows) != 0 || !a[0].UpdatedAt.IsZero() {
		t.Fatal("logout retained old account", a, err)
	}
	write(t, active.identityPath(), identityJSON)
	if _, err := g.Load(context.Background()); err != nil || calls != 2 {
		t.Fatal("new login inherited old cache", calls, err)
	}
}
