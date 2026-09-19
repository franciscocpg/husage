package claude

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func credentialProvider(t *testing.T) (*Provider, *oauthCredentials) {
	t.Helper()
	home := t.TempDir()
	p := New(Options{Home: home, ConfigDir: filepath.Join(home, "profile")})
	write(t, p.opts.identityPath(), `{"oauthAccount":{"accountUuid":"user","organizationUuid":"org"}}`)
	creds := &oauthCredentials{AccessToken: "expired-test-token", RefreshToken: "test-refresh", ExpiresAt: time.Now().Add(-time.Hour).UnixMilli(), Scopes: []string{"user:profile", "user:inference"}}
	p.readCredentials = func(context.Context) (oauthCredentials, error) { return *creds, nil }
	return p, creds
}

func TestExpiredCredentialsAreRenewedAndReloaded(t *testing.T) {
	p, creds := credentialProvider(t)
	renewals := 0
	p.renewCredentials = func(_ context.Context, got oauthCredentials) error {
		renewals++
		if got.RefreshToken != "test-refresh" {
			t.Fatal("wrong profile refreshed")
		}
		creds.AccessToken = "renewed-test-token"
		creds.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		return nil
	}
	requests := 0
	p.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "Bearer renewed-test-token" {
			t.Fatal("usage called with expired token")
		}
		return response(200, `{"five_hour":{"utilization":12}}`), nil
	})
	a, err := p.Load(context.Background())
	if err != nil || len(a) != 1 || a[0].Error != "" || a[0].Windows[0].Used != 12 || renewals != 1 || requests != 1 {
		t.Fatal("renewal did not recover usage", a, err)
	}
	p.Load(context.Background())
	if requests != 1 || renewals != 1 {
		t.Fatal("ignored cooldown")
	}
}

func TestRenewalFailureBackoffAndManualLoginRecovery(t *testing.T) {
	p, creds := credentialProvider(t)
	renewals := 0
	p.renewCredentials = func(context.Context, oauthCredentials) error {
		renewals++
		return errors.New("renewal rejected")
	}
	requests := 0
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return response(200, `{"five_hour":{"utilization":12}}`), nil
	})
	for i := 0; i < 3; i++ {
		a, err := p.Load(context.Background())
		if err != nil || a[0].Error != "renewal rejected" {
			t.Fatal(a, err)
		}
	}
	if renewals != 1 || requests != 0 {
		t.Fatal("retried failed renewal during cooldown")
	}
	creds.AccessToken = "manually-renewed"
	creds.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
	a, err := p.Load(context.Background())
	if err != nil || a[0].Error != "" || requests != 1 || renewals != 1 {
		t.Fatal("manual login required restart", a, err)
	}
}

func TestRenewalPrerequisitesAndConcurrentRenewal(t *testing.T) {
	for _, scenario := range []string{"no refresh token", "no scopes", "locked", "already renewed", "not persisted", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			p, creds := credentialProvider(t)
			calls := 0
			p.renewCredentials = func(context.Context, oauthCredentials) error { calls++; return nil }
			ctx := context.Background()
			switch scenario {
			case "no refresh token":
				creds.RefreshToken = ""
			case "no scopes":
				creds.Scopes = nil
			case "locked":
				_, service := p.credentialLocation()
				write(t, filepath.Join(p.opts.Home, ".config", "husage", "locks", service+".lock"), "another writer")
			case "already renewed":
				creds.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := p.renewToken(ctx, "")
			if scenario == "already renewed" {
				if err != nil || calls != 0 {
					t.Fatal("refreshed an already-renewed credential", err)
				}
			} else if err == nil || (scenario != "not persisted" && calls != 0) {
				t.Fatal("unsafe or unpersisted renewal", err)
			}
		})
	}
}

func TestUnauthorizedRetriesOnceAndForbiddenDoesNotRenew(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			p, creds := credentialProvider(t)
			creds.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
			calls, renewals := 0, 0
			p.renewCredentials = func(context.Context, oauthCredentials) error {
				renewals++
				creds.AccessToken = "new-token"
				return nil
			}
			p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { calls++; return response(status, "secret response"), nil })
			a, err := p.Load(context.Background())
			if err != nil || a[0].Error == "" || strings.Contains(a[0].Error, "secret") {
				t.Fatal(a, err)
			}
			if status == 401 && (calls != 2 || renewals != 1) {
				t.Fatal("unauthorized retry not bounded", calls, renewals)
			}
			if status == 403 && (calls != 1 || renewals != 0) {
				t.Fatal("forbidden response rotated credentials")
			}
		})
	}
}

func TestAccountChangedDuringRenewalDoesNotFetchWrongUsage(t *testing.T) {
	p, creds := credentialProvider(t)
	p.renewCredentials = func(context.Context, oauthCredentials) error {
		creds.AccessToken = "new-token"
		creds.ExpiresAt = time.Now().Add(time.Hour).UnixMilli()
		write(t, p.opts.identityPath(), `{"oauthAccount":{"accountUuid":"other","organizationUuid":"other"}}`)
		return nil
	}
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("read usage under wrong identity")
		return nil, nil
	})
	a, err := p.Load(context.Background())
	if err != nil || !strings.Contains(a[0].Error, "account changed") || len(a[0].Windows) != 0 {
		t.Fatal(a, err)
	}
}

func TestNativeRenewalCommandIsIsolatedAndRedactsFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test executable")
	}
	p, creds := credentialProvider(t)
	p.opts.SecureDir = filepath.Join(p.opts.Home, "separate store")
	bin := t.TempDir()
	script := `#!/bin/sh
[ "$#" = 3 ] && [ "$1" = auth ] && [ "$2" = login ] && [ "$3" = --claudeai ] || exit 41
[ "$CLAUDE_CONFIG_DIR" = "$TEST_CONFIG" ] || exit 42
[ "$CLAUDE_SECURESTORAGE_CONFIG_DIR" = "$TEST_STORE" ] || exit 43
[ "$CLAUDE_CODE_OAUTH_REFRESH_TOKEN" = test-refresh ] || exit 44
[ "$CLAUDE_CODE_OAUTH_SCOPES" = 'user:profile user:inference' ] || exit 45
[ -z "${CLAUDE_CODE_OAUTH_TOKEN+x}" ] && [ -z "${ANTHROPIC_API_KEY+x}" ] && [ -z "${ANTHROPIC_AUTH_TOKEN+x}" ] || exit 46
printf 'private-token-output' >&2
exit "$TEST_EXIT"
`
	write(t, filepath.Join(bin, "claude"), script)
	if err := os.Chmod(filepath.Join(bin, "claude"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("TEST_CONFIG", p.opts.ConfigDir)
	t.Setenv("TEST_STORE", p.opts.SecureDir)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_OAUTH_SCOPES", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
		t.Setenv(key, "unrelated-profile")
	}
	t.Setenv("TEST_EXIT", "0")
	if err := p.renewWithCLI(context.Background(), *creds); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_EXIT", "1")
	err := p.renewWithCLI(context.Background(), *creds)
	if err == nil || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "test-refresh") || !strings.Contains(err.Error(), p.opts.ConfigDir) {
		t.Fatal("unsafe error or missing profile-specific recovery", err)
	}
	if os.Getenv("CLAUDE_CODE_OAUTH_REFRESH_TOKEN") != "unrelated-profile" {
		t.Fatal("changed parent authentication")
	}
}
