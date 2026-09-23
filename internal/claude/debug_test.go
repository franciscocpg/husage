package claude

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRedactRemovesCredentials(t *testing.T) {
	jwt := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4ifQ.c2lnbmF0dXJlLXZhbHVlLWxvbmctZW5vdWdoLXRvLWhpZGU"
	input := strings.Join([]string{
		"known secret-refresh-value here",
		`{"error":"invalid_grant","refresh_token":"abc","access_token": "def","code":"xyz"}`,
		"Authorization: Bearer abc.def",
		"key sk-ant-oat01-abcdef",
		"opaque " + jwt,
		"\x1b[31mred\x1b[0m exit_code=1 scopes=user:profile",
	}, "\n")
	got := redact(input, "secret-refresh-value", "short")
	for _, leaked := range []string{"secret-refresh-value", `"abc"`, `"def"`, `"xyz"`, "abc.def", "sk-ant", "eyJhbGci", "c2lnbmF0dXJl", "\x1b"} {
		if strings.Contains(got, leaked) {
			t.Errorf("leaked %q in %q", leaked, got)
		}
	}
	for _, kept := range []string{"invalid_grant", "exit_code=1", "scopes=user:profile", "red"} {
		if !strings.Contains(got, kept) {
			t.Errorf("lost %q in %q", kept, got)
		}
	}
}

func TestCappedBufferDropsPartialLine(t *testing.T) {
	b := &cappedBuffer{limit: 12}
	if n, err := b.Write([]byte("first line\nsecret-part")); n != 22 || err != nil {
		t.Fatalf("short write: %d %v", n, err)
	}
	if got := b.String(); got != "first line\n[output truncated]" {
		t.Fatalf("unexpected capture %q", got)
	}
}

func TestDebugLogRecordsNativeRenewalFailureWithoutCredentials(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test executable")
	}
	p, _ := credentialProvider(t)
	var log bytes.Buffer
	p.debug = NewDebugLog(&log)
	bin := t.TempDir()
	write(t, filepath.Join(bin, "claude"), `#!/bin/sh
if [ "$1" = --version ]; then echo "9.9.9 (Claude Code)"; exit 0; fi
printf '{"error":"invalid_grant","detail":"%s"}\n' "$CLAUDE_CODE_OAUTH_REFRESH_TOKEN" >&2
echo "OAuth refresh rejected"
exit 3
`)
	if err := os.Chmod(filepath.Join(bin, "claude"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if _, err := p.renewToken(context.Background(), ""); err == nil {
		t.Fatal("renewal unexpectedly succeeded")
	}
	got := log.String()
	for _, want := range []string{"profile=" + p.opts.ConfigDir, "renewal starting (access token expired or missing)", "refresh=present", "9.9.9 (Claude Code)", "exit status 3", "invalid_grant", "OAuth refresh rejected", "renewal failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in log:\n%s", want, got)
		}
	}
	for _, leaked := range []string{"test-refresh", "expired-test-token"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("leaked %q in log:\n%s", leaked, got)
		}
	}
}

func TestDebugLogShowsRenewalThatDropsRefreshToken(t *testing.T) {
	p, creds := credentialProvider(t)
	var log bytes.Buffer
	p.debug = NewDebugLog(&log)
	p.renewCredentials = func(context.Context, oauthCredentials) error {
		creds.AccessToken, creds.RefreshToken, creds.ExpiresAt = "renewed-access-token", "", 0
		return nil
	}
	if _, err := p.renewToken(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	got := log.String()
	if !strings.Contains(got, "credentials after renewal: access=present refresh=missing expires=none") || !strings.Contains(got, "access_changed=true refresh_changed=true") {
		t.Fatalf("renewed credential state not logged:\n%s", got)
	}
	if strings.Contains(got, "renewed-access-token") || strings.Contains(got, "test-refresh") {
		t.Fatalf("leaked credentials:\n%s", got)
	}
}

func TestProfilesShareDebugLogWithAddedProviders(t *testing.T) {
	home := t.TempDir()
	g := NewProfiles(Options{Home: home}, []Options{{Home: home}})
	d := NewDebugLog(&bytes.Buffer{})
	g.SetDebugLog(d)
	g.Add(Options{Home: home, ConfigDir: filepath.Join(home, "second")})
	for _, p := range g.providers {
		if p.debug != d {
			t.Fatalf("provider %s has no debug log", p.opts.ConfigDir)
		}
	}
}
