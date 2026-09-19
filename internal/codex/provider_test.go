package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const usageFixture = `{"accountId":"workspace-one","rateLimits":{"primary":{"usedPercent":15,"windowDurationMins":300,"resetsAt":1790000000}},"rateLimitsByLimitId":{"codex":{"primary":{"usedPercent":15,"windowDurationMins":300,"resetsAt":1790000000},"secondary":{"usedPercent":29,"windowDurationMins":10080,"resetsAt":1790005000}},"code_review":{"limitName":"Code review","primary":{"usedPercent":0,"windowDurationMins":10080}}}}`

func sampleReading(t *testing.T) reading {
	t.Helper()
	r := reading{Account: &accountInfo{Type: "chatgpt", Email: "same@example.com", Plan: "pro"}}
	if err := json.Unmarshal([]byte(usageFixture), &r.Limits); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRateLimitWindows(t *testing.T) {
	r := sampleReading(t)
	w, err := r.Limits.windows()
	if err != nil || len(w) != 3 {
		t.Fatal(w, err)
	}
	labels := map[string]bool{}
	for _, v := range w {
		labels[v.Label] = true
		if v.Used == 15 && v.ResetsAt.Unix() != 1790000000 {
			t.Fatal("wrong reset units")
		}
	}
	for _, label := range []string{"Current session (5h)", "Current week", "Current week (Code review)"} {
		if !labels[label] {
			t.Fatal("missing window", label)
		}
	}
	r.Limits.Buckets = nil
	w, err = r.Limits.windows()
	if err != nil || len(w) != 1 || w[0].Used != 15 {
		t.Fatal("legacy single bucket failed", w, err)
	}
	for _, body := range []string{`{}`, `{"rateLimits":{"primary":{}}}`, `{"rateLimits":{"primary":{"usedPercent":-1}}}`, `{"rateLimits":{"primary":{"usedPercent":101}}}`} {
		var limits rateLimits
		if json.Unmarshal([]byte(body), &limits) != nil {
			t.Fatal(body)
		}
		if _, err := limits.windows(); err == nil {
			t.Fatal("accepted invalid usage", body)
		}
	}
}

func TestCodexProfilesIsolationAndCooldown(t *testing.T) {
	g := NewProfiles([]Options{{Home: t.TempDir()}, {Home: t.TempDir(), Active: true}, {Home: t.TempDir()}})
	calls := 0
	now := time.Now()
	for i, p := range g.providers {
		r := sampleReading(t)
		if i == 1 {
			r.Limits.AccountID = "workspace-two"
		}
		p.now = func() time.Time { return now }
		p.read = func(_ context.Context, home string) (reading, error) {
			calls++
			if home != p.opts.Home {
				t.Fatal("wrong home")
			}
			return r, nil
		}
	}
	a, err := g.Load(context.Background())
	if err != nil || len(a) != 2 || !a[0].Active || !strings.Contains(a[0].ID, "workspace-two") {
		t.Fatal(a, err)
	}
	if a[0].ID == a[1].ID {
		t.Fatal("merged distinct workspaces with the same email")
	}
	g.Load(context.Background())
	if calls != 3 {
		t.Fatal("ignored per-home cooldown")
	}
	now = now.Add(5 * time.Minute)
	g.providers[1].read = func(context.Context, string) (reading, error) { return reading{}, errors.New("temporary failure") }
	a, err = g.Load(context.Background())
	if err != nil || len(a) != 2 || a[0].Error == "" || a[1].Error != "" {
		t.Fatal("one failed home hid healthy accounts", a, err)
	}
	if len(a[0].Windows) != 0 {
		t.Fatal("unverified account inherited old usage")
	}
}

func TestMissingLoggedOutAndAPIKeyHomes(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for _, optional := range []bool{true, false} {
		p := New(Options{Home: missing, Optional: optional})
		a, err := p.Load(context.Background())
		if err != nil || optional && len(a) != 0 || !optional && (len(a) != 1 || a[0].Error == "") {
			t.Fatal(a, err)
		}
	}
	for _, kind := range []string{"", "apiKey"} {
		p := New(Options{Home: t.TempDir()})
		p.read = func(context.Context, string) (reading, error) {
			r := reading{}
			if kind != "" {
				r.Account = &accountInfo{Type: kind}
			}
			return r, nil
		}
		a, err := p.Load(context.Background())
		if err != nil || len(a) != 1 || a[0].Error == "" || len(a[0].Windows) != 0 {
			t.Fatal(a, err)
		}
		if !strings.Contains(a[0].Error, "CODEX_HOME=") {
			t.Fatal("missing scoped recovery command")
		}
	}
}

func TestNativeLoginChangeInvalidatesCachedAccount(t *testing.T) {
	p := New(Options{Home: t.TempDir()})
	now := time.Now()
	p.now = func() time.Time { return now }
	r := sampleReading(t)
	calls := 0
	p.read = func(context.Context, string) (reading, error) {
		calls++
		return r, nil
	}
	first, err := p.Load(context.Background())
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	r.Limits.AccountID = "another-workspace"
	if err := os.WriteFile(filepath.Join(p.opts.Home, "auth.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := p.Load(context.Background())
	if err != nil || len(second) != 1 || first[0].ID == second[0].ID || calls != 2 {
		t.Fatal("native login change retained the previous account", second, calls, err)
	}
	p.Load(context.Background())
	if calls != 2 {
		t.Fatal("unchanged credentials bypassed the cooldown")
	}
}
