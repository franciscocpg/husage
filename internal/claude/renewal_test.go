package claude

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeClaude(t *testing.T, output string) (calls func() int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test executable")
	}
	bin := t.TempDir()
	count := filepath.Join(bin, "calls")
	write(t, filepath.Join(bin, "claude"), `#!/bin/sh
[ "$1" = --version ] && exit 0
echo x >> "`+count+`"
echo "`+output+`"
exit 1
`)
	if err := os.Chmod(filepath.Join(bin, "claude"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return func() int {
		data, _ := os.ReadFile(count)
		return strings.Count(string(data), "x")
	}
}

func TestRejectedRefreshTokenRequiresLoginAndIsNotRetried(t *testing.T) {
	p, creds := credentialProvider(t)
	calls := fakeClaude(t, "Login failed: Request failed with status code 400")
	a, err := p.Load(context.Background())
	if err != nil || !a[0].LoginRequired || a[0].Warning != loginWarning || !strings.Contains(a[0].Error, "rejected this login's refresh token") {
		t.Fatalf("rejected refresh token not reported as login required: %+v %v", a, err)
	}
	p.nextFetch = time.Time{}
	if a, _ = p.Load(context.Background()); !a[0].LoginRequired || calls() != 1 {
		t.Fatalf("retried a rejected refresh token: %d calls, %+v", calls(), a[0])
	}
	creds.RefreshToken = "fresh-login-refresh"
	p.nextFetch = time.Time{}
	p.Load(context.Background())
	if calls() != 2 {
		t.Fatal("new login was not renewed")
	}
}

func TestRenewalTimeoutIsRetried(t *testing.T) {
	p, _ := credentialProvider(t)
	calls := fakeClaude(t, "Login failed: timeout of 30000ms exceeded")
	a, _ := p.Load(context.Background())
	if a[0].LoginRequired {
		t.Fatal("timeout treated as rejected login")
	}
	p.nextFetch = time.Time{}
	p.Load(context.Background())
	if calls() != 2 {
		t.Fatalf("timeout was not retried: %d calls", calls())
	}
}

func TestRenewalWaitsAfterSystemSleep(t *testing.T) {
	p, _ := credentialProvider(t)
	now := time.Now()
	p.now = func() time.Time { return now }
	renewals := 0
	p.renewCredentials = func(context.Context, oauthCredentials) error { renewals++; return errors.New("offline") }
	p.lastSeen = now.Add(-5 * time.Minute)
	p.sleepGap = func(time.Time, time.Time) time.Duration { return 4 * time.Minute }
	a, _ := p.Load(context.Background())
	if renewals != 0 || a[0].LoginRequired || a[0].Warning != errSettling.warning || !p.nextFetch.Equal(now.Add(settleAfterWake)) {
		t.Fatalf("renewed right after wake: %d %+v next=%s", renewals, a[0], p.nextFetch)
	}
	p.sleepGap = func(time.Time, time.Time) time.Duration { return 0 }
	now = now.Add(time.Minute)
	p.Load(context.Background())
	if renewals != 0 {
		t.Fatal("renewed before the system settled")
	}
	now = now.Add(settleAfterWake)
	p.Load(context.Background())
	if renewals != 1 {
		t.Fatal("renewal did not resume after settling")
	}
}

func TestSystemSleepIgnoresAwakeIntervals(t *testing.T) {
	start := time.Now()
	if slept := systemSleep(start, start.Add(time.Minute)); slept != 0 {
		t.Fatalf("awake interval reported as sleep: %s", slept)
	}
	if slept := systemSleep(start.Round(0), start.Round(0).Add(10*time.Minute)); slept != 0 {
		t.Fatalf("wall-only times reported sleep: %s", slept)
	}
}

func TestAccessTokenIsRenewedAheadOfExpiry(t *testing.T) {
	p, creds := credentialProvider(t)
	creds.AccessToken = "expiring-test-token"
	creds.ExpiresAt = time.Now().Add(30 * time.Minute).UnixMilli()
	var renewErr error
	renewals := 0
	p.renewCredentials = func(context.Context, oauthCredentials) error {
		renewals++
		if renewErr == nil {
			creds.AccessToken, creds.RefreshToken = "renewed-test-token", "rotated-refresh"
			creds.ExpiresAt = time.Now().Add(8 * time.Hour).UnixMilli()
		}
		return renewErr
	}
	var used []string
	p.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		used = append(used, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		return response(200, `{"five_hour":{"utilization":12}}`), nil
	})
	if a, _ := p.Load(context.Background()); a[0].Error != "" || renewals != 1 || used[0] != "renewed-test-token" {
		t.Fatalf("expiring token not renewed ahead: %d %v %+v", renewals, used, a[0])
	}
	p.nextFetch = time.Time{}
	p.Load(context.Background())
	if renewals != 1 {
		t.Fatal("renewed a token that is not close to expiry")
	}

	creds.AccessToken, creds.ExpiresAt = "expiring-again", time.Now().Add(30*time.Minute).UnixMilli()
	renewErr = errors.New("offline")
	p.nextFetch = time.Time{}
	if a, _ := p.Load(context.Background()); a[0].Error != "" || renewals != 2 || used[len(used)-1] != "expiring-again" {
		t.Fatalf("failed early renewal hid a still-valid token: %v %+v", used, a[0])
	}

	p.awakeSince = p.now()
	p.nextFetch = time.Time{}
	p.Load(context.Background())
	if renewals != 2 || used[len(used)-1] != "expiring-again" {
		t.Fatal("renewed ahead of expiry right after wake")
	}
}
