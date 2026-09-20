package cursor

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

type Options struct {
	Home               string
	Optional, Disabled bool
}
type Provider struct {
	mu             sync.Mutex
	opts           Options
	client         *http.Client
	url            string
	now            func() time.Time
	token          func(context.Context, string) (string, error)
	next           time.Time
	credentialHash [32]byte
	cached         []subscription.Account
}

func New(opts Options) *Provider {
	return &Provider{opts: opts, url: dashboardURL, now: time.Now, token: readToken, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (p *Provider) Load(ctx context.Context) ([]subscription.Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.opts.Disabled {
		return nil, nil
	}
	token, err := p.token(ctx, NativeDirectory(p.opts.Home))
	if err != nil {
		// Never attribute a previously signed-in account to an unverified login.
		p.cached = nil
		p.credentialHash = [32]byte{}
		p.next = time.Time{}
		if p.opts.Optional && errors.Is(err, errNoLogin) {
			return nil, nil
		}
		p.cached = []subscription.Account{{ID: "cursor:current", Provider: "Cursor", Name: "Cursor", Active: true, Source: "Cursor API", Error: err.Error()}}
		p.cached[0].Login = &subscription.LoginTarget{Provider: "cursor"}
		p.cached[0].LoginRequired = errors.Is(err, errNoLogin)
		return p.cached, nil
	}
	hash := sha256.Sum256([]byte(token))
	if hash == p.credentialHash && p.now().Before(p.next) {
		return p.cached, nil
	}
	if hash != p.credentialHash {
		p.cached = nil
	}
	p.credentialHash = hash
	p.next = p.now().Add(5 * time.Minute)
	a := subscription.Account{ID: "cursor:current", Provider: "Cursor", Name: "Cursor", Active: true, Source: "Cursor API"}
	a.Login = &subscription.LoginTarget{Provider: "cursor"}
	var id identity
	err = p.call(ctx, "GetMe", token, &id)
	if err == nil && !id.valid() {
		err = errors.New("Cursor returned no account identity.")
	}
	if err == nil {
		a.ID, a.Email = id.key(), id.Email
		if id.TeamName != "" {
			a.Name = id.TeamName
		}
		// The server-verified user/team identity gates cache reuse across restarts.
		p.restoreUsage(&a)
		var usage usage
		err = p.call(ctx, "GetCurrentPeriodUsage", token, &usage)
		if err == nil {
			var windows []subscription.Window
			windows, err = usage.windows()
			if err == nil {
				a.Windows, a.UpdatedAt = windows, p.now()
				var plan struct {
					Info *struct {
						Name string `json:"planName"`
					} `json:"planInfo"`
				}
				if p.call(ctx, "GetPlanInfo", token, &plan) == nil && plan.Info != nil && plan.Info.Name != "" {
					a.Name += " · " + plan.Info.Name
				}
				if p.saveUsage(a) != nil {
					a.Error = "Usage loaded, but the local cache could not be saved."
				}
			}
		}
	}
	if err != nil {
		// A transient identity lookup failure can use only this process's cache
		// for the exact same credential; a different token clears it above.
		if len(p.cached) > 0 && (a.ID == "cursor:current" || a.ID == p.cached[0].ID) {
			a = p.cached[0]
		}
		a.Error = err.Error()
		a.Stale = len(a.Windows) > 0
	}
	p.cached = []subscription.Account{a}
	return p.cached, nil
}

func (p *Provider) Remove(id string, save func([]string) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.opts.Disabled || len(p.cached) == 0 || p.cached[0].ID != id {
		return errors.New("This subscription changed. Close this screen and select it again.")
	}
	if err := save([]string{NativeDirectory(p.opts.Home)}); err != nil {
		return err
	}
	p.opts.Disabled = true
	// Keep the in-memory read/cooldown for a later restore. Load still checks
	// the native credential fingerprint before using it after re-enabling.
	return nil
}

// Enable restores the current login only after the selected profile list is
// saved. It neither runs login nor changes the native credential store.
func (p *Provider) Enable(ctx context.Context, save func() error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := p.token(ctx, NativeDirectory(p.opts.Home)); err != nil {
		return err
	}
	if err := save(); err != nil {
		return err
	}
	p.opts.Disabled, p.opts.Optional = false, false
	if len(p.cached) == 0 {
		p.next = time.Time{}
	}
	return nil
}
