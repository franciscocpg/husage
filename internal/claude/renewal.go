package claude

import (
	"crypto/sha256"
	"regexp"
	"time"
)

const (
	renewAhead      = time.Hour
	sleepThreshold  = 30 * time.Second
	settleAfterWake = 2 * time.Minute
)

var rejectedRefreshPattern = regexp.MustCompile(`(?i)status code 40[01]\b|invalid_grant`)

func refreshRejected(output string) bool { return rejectedRefreshPattern.MatchString(output) }

func fingerprint(token string) [32]byte { return sha256.Sum256([]byte(token)) }

func systemSleep(prev, now time.Time) time.Duration {
	return now.Round(0).Sub(prev.Round(0)) - now.Sub(prev)
}

func (p *Provider) observeClock() {
	now := p.now()
	if !p.lastSeen.IsZero() {
		if slept := p.sleepGap(p.lastSeen, now); slept > sleepThreshold {
			p.awakeSince = now
			p.debugf(nil, "system slept for about %s; renewals wait %s after waking", slept.Round(time.Second), settleAfterWake)
		}
	}
	p.lastSeen = now
}

func (p *Provider) settling() bool {
	return !p.awakeSince.IsZero() && p.now().Sub(p.awakeSince) < settleAfterWake
}

func (p *Provider) expiresSoon(c oauthCredentials) bool {
	return c.ExpiresAt > 0 && time.UnixMilli(c.ExpiresAt).Sub(p.now()) < renewAhead
}

var errSettling = recoveryError{
	detail:  "Claude login renewal is waiting for the system to settle after sleep. Retrying on the next reload.",
	warning: "Login renewal paused after sleep; retrying shortly.",
}
