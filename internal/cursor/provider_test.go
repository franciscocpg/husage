package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

const fixture = `{"billingCycleEnd":"1792256348000","planUsage":{"includedSpend":1298,"limit":2000,"totalPercentUsed":5.192,"autoPercentUsed":6.18095238095238,"apiPercentUsed":0},"spendLimitUsage":{"limitType":"team"}}`

func TestUsageWindowsUseReportedPercentagesAndMilliseconds(t *testing.T) {
	var u usage
	if err := json.Unmarshal([]byte(fixture), &u); err != nil {
		t.Fatal(err)
	}
	w, err := u.windows()
	if err != nil || len(w) != 3 || w[0].Used != 5.192 || w[1].Used != 6.18095238095238 || w[2].Used != 0 || w[0].ResetsAt.UnixMilli() != 1792256348000 {
		t.Fatal(w, err)
	}
	for _, tc := range []struct {
		body string
		used float64
	}{
		{`{"planUsage":{"includedSpend":50,"limit":200}}`, 25},
		{`{"planUsage":{"limit":200}}`, 0},
		{`{"planUsage":{"totalPercentUsed":110}}`, 110},
		{`{"spendLimitUsage":{"individualUsed":25,"individualLimit":100}}`, 25},
		{`{"spendLimitUsage":{"individualLimit":100}}`, 0},
	} {
		var u usage
		if err := json.Unmarshal([]byte(tc.body), &u); err != nil {
			t.Fatal(err)
		}
		w, err := u.windows()
		if err != nil || len(w) != 1 || w[0].Used != tc.used || !w[0].ResetsAt.IsZero() {
			t.Fatal(w, err)
		}
	}
	for _, body := range []string{`{}`, `{"planUsage":{}}`, `{"planUsage":{"limit":0}}`, `{"planUsage":{"totalPercentUsed":-1}}`, `{"billingCycleEnd":"bad","planUsage":{"totalPercentUsed":1}}`, `{"spendLimitUsage":{"pooledLimit":1000,"pooledUsed":10}}`} {
		var u usage
		if err := json.Unmarshal([]byte(body), &u); err != nil {
			continue
		}
		if _, err := u.windows(); err == nil {
			t.Fatal("invented or accepted invalid limits", body)
		}
	}
}

func TestCacheCooldownFailuresAndAccountIsolation(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	team, token := "1", "token-one"
	usageStatus, meStatus := 200, 200
	calls := 0
	newProvider := func() *Provider {
		p := New(Options{Home: home})
		p.now = func() time.Time { return now }
		p.token = func(context.Context, string) (string, error) { return token, nil }
		p.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || string(body) != "{}" || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Connect-Protocol-Version") != "1" || r.Header.Get("Content-Type") != "application/json" {
				t.Fatal("incorrect usage RPC")
			}
			switch r.URL.Path {
			case "/aiserver.v1.DashboardService/GetMe":
				return response(meStatus, `{"authId":"user","userId":7,"teamId":`+team+`,"email":"sample@example.com","teamName":"Example"}`), nil
			case "/aiserver.v1.DashboardService/GetCurrentPeriodUsage":
				r := response(usageStatus, fixture)
				if usageStatus == 429 {
					r.Header.Set("Retry-After", "900")
				}
				return r, nil
			case "/aiserver.v1.DashboardService/GetPlanInfo":
				return response(200, `{"planInfo":{"planName":"Team"}}`), nil
			default:
				t.Fatal("unexpected endpoint", r.URL.Path)
				return nil, errors.New("unexpected endpoint")
			}
		})
		return p
	}
	p := newProvider()
	a, err := p.Load(context.Background())
	if err != nil || len(a) != 1 || a[0].Error != "" || a[0].Name != "Example · Team" || a[0].Windows[0].Used != 5.192 || calls != 3 {
		t.Fatal(a, err, calls)
	}
	first := a[0]
	cache, _ := p.cachePath(first.ID)
	data, _ := os.ReadFile(cache)
	info, _ := os.Stat(cache)
	if info == nil || info.Mode().Perm() != 0600 || strings.Contains(string(data), token) || strings.Contains(string(data), "sample@example.com") {
		t.Fatal("unsafe cache")
	}
	p.Load(context.Background())
	if calls != 3 {
		t.Fatal("ignored cooldown")
	}
	now = now.Add(5 * time.Minute)
	usageStatus = 429
	p = newProvider() // No memory cache after restart.
	a, err = p.Load(context.Background())
	if err != nil || len(a) != 1 || !a[0].Stale || len(a[0].Windows) != 3 || !a[0].UpdatedAt.Equal(first.UpdatedAt) || !p.next.Equal(now.Add(15*time.Minute)) {
		t.Fatal(a, err)
	}
	before := calls
	now = now.Add(6 * time.Minute)
	p.Load(context.Background())
	if calls != before {
		t.Fatal("ignored rate limit backoff")
	}
	team, token = "2", "token-two" // Same email, different team and credentials.
	a, err = p.Load(context.Background())
	if err != nil || len(a[0].Windows) != 0 || a[0].Stale || a[0].ID == first.ID {
		t.Fatal("cross-account usage leak", a, err)
	}
	// Returning to the first team recovers its successful disk cache.
	team, token = "1", "token-one"
	a, err = p.Load(context.Background())
	if err != nil || !a[0].Stale || len(a[0].Windows) != 3 {
		t.Fatal(a, err)
	}
	now = now.Add(16 * time.Minute)
	usageStatus = 200
	a, err = p.Load(context.Background())
	if err != nil || a[0].Error != "" || a[0].Stale || !a[0].UpdatedAt.Equal(now) {
		t.Fatal(a, err)
	}
	// A transient identity endpoint failure retains only this credential's memory cache.
	now = now.Add(5 * time.Minute)
	meStatus = 500
	a, err = p.Load(context.Background())
	if err != nil || !a[0].Stale || len(a[0].Windows) != 3 {
		t.Fatal(a, err)
	}
	p.token = func(context.Context, string) (string, error) { return "", errNoLogin }
	a, err = p.Load(context.Background())
	if err != nil || len(a) != 1 || a[0].Stale || len(a[0].Windows) != 0 {
		t.Fatal("logged-out account retained usage", a, err)
	}
}

func TestOptionalMissingAndDisabledCursor(t *testing.T) {
	for _, opts := range []Options{{Home: t.TempDir(), Optional: true}, {Home: t.TempDir(), Disabled: true}} {
		p := New(opts)
		p.token = func(context.Context, string) (string, error) {
			if opts.Disabled {
				t.Fatal("read credentials for disabled provider")
			}
			return "", errNoLogin
		}
		a, err := p.Load(context.Background())
		if err != nil || len(a) != 0 {
			t.Fatal(a, err)
		}
	}
}

func TestErrorsAreRedactedAndRedirectsRejected(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500, 302} {
		p := New(Options{})
		p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return response(status, "private-server-body"), nil })
		var u usage
		err := p.call(context.Background(), "GetCurrentPeriodUsage", "private-token", &u)
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
		if err := p.client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
			t.Fatal("credentials could follow redirects")
		}
	}
}

func TestNativeAuthFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if _, err := readTokenFile(path); !errors.Is(err, errNoLogin) {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(`{"accessToken":"test-oauth","refreshToken":"never-read"}`), 0600)
	if token, err := readTokenFile(path); err != nil || token != "test-oauth" {
		t.Fatal("could not read native auth")
	}
	for _, body := range []string{`{private`, `{"apiKey":"private-api-key"}`} {
		os.WriteFile(path, []byte(body), 0600)
		if _, err := readTokenFile(path); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
	}
}

func TestCursorRemovalPersistsBeforeHiding(t *testing.T) {
	p := New(Options{Home: t.TempDir()})
	p.cached = []subscription.Account{{ID: "cursor:account", Provider: "Cursor"}}
	if err := p.Remove("cursor:account", func([]string) error { return errors.New("locked") }); err == nil || p.opts.Disabled {
		t.Fatal("failed save removed account")
	}
	if err := p.Remove("different-account", func([]string) error { t.Fatal("saved stale selection"); return nil }); err == nil {
		t.Fatal("stale selection accepted")
	}
	if err := p.Remove("cursor:account", func(dirs []string) error {
		if len(dirs) != 1 || dirs[0] != NativeDirectory(p.opts.Home) {
			t.Fatal(dirs)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p.token = func(context.Context, string) (string, error) {
		t.Fatal("removed account read credentials")
		return "", nil
	}
	a, err := p.Load(context.Background())
	if err != nil || len(a) != 0 {
		t.Fatal(a, err)
	}
}

func TestEnableRestoresCursorWithoutNewLoginOrBypassingCooldown(t *testing.T) {
	p := New(Options{Home: t.TempDir(), Disabled: true, Optional: true})
	p.token = func(context.Context, string) (string, error) { return "", errNoLogin }
	saved := 0
	save := func() error { saved++; return nil }
	if err := p.Enable(context.Background(), save); err == nil || saved != 0 || !p.opts.Disabled {
		t.Fatal("enabled absent login")
	}
	p.token = func(context.Context, string) (string, error) { return "existing-token", nil }
	if err := p.Enable(context.Background(), func() error { return errors.New("locked") }); err == nil || !p.opts.Disabled {
		t.Fatal("failed save enabled provider")
	}
	p.cached = []subscription.Account{{ID: "cursor:existing", Provider: "Cursor"}}
	p.credentialHash = sha256.Sum256([]byte("existing-token"))
	p.next = time.Now().Add(5 * time.Minute)
	if err := p.Enable(context.Background(), save); err != nil || p.opts.Disabled || p.opts.Optional {
		t.Fatal(err)
	}
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("restore bypassed usage cooldown")
		return nil, nil
	})
	a, err := p.Load(context.Background())
	if err != nil || len(a) != 1 || a[0].ID != "cursor:existing" {
		t.Fatal(a, err)
	}
	if err := p.Remove(a[0].ID, func([]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := p.Enable(context.Background(), save); err != nil {
		t.Fatal(err)
	}
	a, err = p.Load(context.Background())
	if err != nil || len(a) != 1 {
		t.Fatal("removed account did not return immediately", a, err)
	}
}
