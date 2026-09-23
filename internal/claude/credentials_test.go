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

func TestAuthenticationWarningPreservesUsageAndClearsAfterLogin(t *testing.T) {
	p, creds := credentialProvider(t)
	creds.ExpiresAt = time.Now().Add(2 * time.Hour).UnixMilli()
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `{"five_hour":{"utilization":12}}`), nil
	})
	first, err := p.Load(context.Background())
	if err != nil || len(first) != 1 || first[0].Error != "" {
		t.Fatalf("initial load: %v %v", first, err)
	}
	p.readCredentials = func(context.Context) (oauthCredentials, error) {
		return oauthCredentials{}, p.loginError("Saved Claude login is incomplete.")
	}
	p.nextFetch = time.Time{}
	failed, _ := p.Load(context.Background())
	a := failed[0]
	if a.Warning != loginWarning || !a.Stale || len(a.Windows) != 1 || !a.UpdatedAt.Equal(first[0].UpdatedAt) {
		t.Fatalf("missing compact authentication warning with cached usage: %+v", a)
	}
	if !strings.Contains(a.Error, "CLAUDE_CONFIG_DIR=") {
		t.Fatal("detailed recovery command lost")
	}
	// Fresh native credentials are detected even during the failed-login cooldown.
	creds.AccessToken = "new-test-login"
	p.readCredentials = func(context.Context) (oauthCredentials, error) { return *creds, nil }
	recovered, _ := p.Load(context.Background())
	if recovered[0].Warning != "" || recovered[0].Error != "" || recovered[0].Stale {
		t.Fatalf("successful login retained warning: %+v", recovered[0])
	}
}

func TestRecoveryWarningsDoNotMisdiagnoseAccessErrors(t *testing.T) {
	p := New(Options{Home: t.TempDir()})
	for _, tc := range []struct {
		err  error
		want string
	}{
		{credentialLoginError("Missing tokens"), loginWarning},
		{fmt.Errorf("wrapped: %w", p.loginError("Missing refresh token")), loginWarning},
		{errUsageUnauthorized, loginWarning},
		{os.ErrPermission, ""},
		{credentialContextError(context.DeadlineExceeded), ""},
		{errors.New("Cannot read Claude credentials from macOS Keychain"), ""},
	} {
		if got := recoveryWarning(tc.err); got != tc.want {
			t.Errorf("%v: warning %q, want %q", tc.err, got, tc.want)
		}
	}
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(403, ""), nil })
	_, err := p.fetch(context.Background(), "test-token")
	if got := recoveryWarning(err); !strings.Contains(got, "permissions") || strings.Contains(got, "Sign in") {
		t.Fatalf("access denied misdiagnosed: %q", got)
	}
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
		creds.ExpiresAt = time.Now().Add(2 * time.Hour).UnixMilli()
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
	creds.ExpiresAt = time.Now().Add(2 * time.Hour).UnixMilli()
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
				creds.ExpiresAt = time.Now().Add(2 * time.Hour).UnixMilli()
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
			creds.ExpiresAt = time.Now().Add(2 * time.Hour).UnixMilli()
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
		creds.ExpiresAt = time.Now().Add(2 * time.Hour).UnixMilli()
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

func TestCredentialStoreFailuresAreDistinctAndRedacted(t *testing.T) {
	valid := []byte(`{"claudeAiOauth":{"accessToken":"private-access","refreshToken":"private-refresh","scopes":["user:profile"]}}`)
	for _, tc := range []struct {
		name     string
		keyData  []byte
		keyErr   error
		fileData []byte
		fileErr  error
		want     string
		login    bool
	}{
		{name: "missing both stores", keyErr: os.ErrNotExist, fileErr: os.ErrNotExist, want: "No Claude login found", login: true},
		{name: "keychain unavailable", keyErr: errors.New("Cannot read Claude credentials from macOS Keychain (exit 36)."), fileErr: os.ErrNotExist, want: "Keychain (exit 36)"},
		{name: "file permission denied", keyErr: os.ErrNotExist, fileErr: os.ErrPermission, want: "permission denied"},
		{name: "file read failed", keyErr: os.ErrNotExist, fileErr: errors.New("private diagnostic"), want: "readable file"},
		{name: "malformed keychain", keyData: []byte(`{"private-access":`), fileData: valid, want: "macOS Keychain contains invalid credential data", login: true},
		{name: "empty keychain", keyData: []byte{}, fileErr: os.ErrNotExist, want: "Keychain is empty", login: true},
		{name: "invalid credential type", keyErr: os.ErrNotExist, fileData: []byte(`{"claudeAiOauth":{"accessToken":["private-access"]}}`), want: "credential file contains invalid credential data", login: true},
		{name: "missing OAuth object", keyData: []byte(`{"other":"private-access"}`), want: "missing OAuth data", login: true},
		{name: "incomplete saved login", keyData: []byte(`{"claudeAiOauth":{"scopes":["user:profile"],"expiresAt":1}}`), want: "access and refresh tokens are missing", login: true},
		{name: "native keychain login", keyData: valid},
		{name: "file fallback after missing item", keyErr: os.ErrNotExist, fileData: valid},
		{name: "file fallback after denied access", keyErr: errors.New("Keychain access denied"), fileData: valid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fileReads := 0
			creds, err := readCredentialStores(context.Background(), func(context.Context) ([]byte, error) { return tc.keyData, tc.keyErr }, func() ([]byte, error) { fileReads++; return tc.fileData, tc.fileErr })
			if tc.want == "" {
				if err != nil || creds.AccessToken != "private-access" {
					t.Fatal("valid credential source was not read", err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatal("wrong diagnostic", err)
				}
				if strings.Contains(err.Error(), "private") {
					t.Fatal("diagnostic exposed credential content")
				}
				var loginErr credentialLoginError
				if errors.As(err, &loginErr) != tc.login {
					t.Fatal("incorrect login recovery advice", err)
				}
			}
			if len(tc.keyData) > 0 && tc.keyErr == nil && fileReads != 0 {
				t.Fatal("read a stale file fallback over existing Keychain data")
			}
		})
	}
}

func TestCredentialLookupCancellationDoesNotRequestLogin(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if timeout {
			cancel()
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		} else {
			cancel()
		}
		_, err := readCredentialStores(ctx, nil, func() ([]byte, error) { t.Fatal("read after cancellation"); return nil, nil })
		cancel()
		var loginErr credentialLoginError
		if err == nil || errors.As(err, &loginErr) {
			t.Fatal("cancelled lookup reported missing login", err)
		}
		expected := "cancelled"
		if timeout {
			expected = "timed out"
		}
		if !strings.Contains(err.Error(), expected) {
			t.Fatal(err)
		}
	}
}

func TestRefreshTokenCanRecoverMissingAccessToken(t *testing.T) {
	p, _ := credentialProvider(t)
	data := []byte(`{"claudeAiOauth":{"refreshToken":"test-refresh","scopes":["user:profile"]}}`)
	p.readCredentials = func(context.Context) (oauthCredentials, error) { return decodeCredentials(data, "test store") }
	renewals := 0
	p.renewCredentials = func(_ context.Context, creds oauthCredentials) error {
		renewals++
		if creds.AccessToken != "" || creds.RefreshToken != "test-refresh" {
			t.Fatal("unexpected renewal input")
		}
		data = []byte(`{"claudeAiOauth":{"accessToken":"renewed-token","refreshToken":"new-refresh","scopes":["user:profile"]}}`)
		return nil
	}
	token, err := p.token(context.Background())
	if err != nil || token != "renewed-token" || renewals != 1 {
		t.Fatal("refresh-only credentials were not recovered", err)
	}
	// Missing both tokens must stop before attempting a renewal without credentials.
	data = []byte(`{"claudeAiOauth":{"scopes":["user:profile"]}}`)
	_, err = p.token(context.Background())
	if err == nil || !strings.Contains(err.Error(), "access and refresh tokens are missing") || renewals != 1 {
		t.Fatal("incomplete login did not stop safely", err)
	}
}
