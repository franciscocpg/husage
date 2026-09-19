package claude

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/franciscocpg/husage/internal/claudecli"
)

type oauthCredentials struct {
	AccessToken  string   `json:"accessToken"`
	RefreshToken string   `json:"refreshToken"`
	ExpiresAt    int64    `json:"expiresAt"`
	Scopes       []string `json:"scopes"`
}

func (c oauthCredentials) expired(now time.Time) bool {
	return c.ExpiresAt > 0 && now.UnixMilli() >= c.ExpiresAt
}

func (p *Provider) credentialLocation() (dir, service string) {
	dir = p.opts.SecureDir
	if dir == "" {
		dir = p.opts.ConfigDir
	}
	service = "Claude Code-credentials"
	if dir != "" {
		service += fmt.Sprintf("-%x", sha256.Sum256([]byte(dir)))[:9]
	} else {
		dir = filepath.Join(p.opts.Home, ".claude")
	}
	return dir, service
}

func (p *Provider) credentials(ctx context.Context) (oauthCredentials, error) {
	if p.readCredentials != nil {
		return p.readCredentials(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	dir, service := p.credentialLocation()
	var data []byte
	if runtime.GOOS == "darwin" {
		// Secrets stay in memory; subprocess output is never logged.
		data, _ = exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", service, "-w").Output()
	}
	if len(data) == 0 {
		data, _ = os.ReadFile(filepath.Join(dir, ".credentials.json"))
	}
	var stored struct {
		OAuth oauthCredentials `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &stored) != nil || stored.OAuth.AccessToken == "" {
		return oauthCredentials{}, p.loginError("No Claude login found for this profile.")
	}
	return stored.OAuth, nil
}

func (p *Provider) loginError(reason string) error {
	command := strings.ReplaceAll(claudecli.LoginCommand(p.opts.ConfigDir, p.opts.SecureDir), " \\\n", " ")
	return fmt.Errorf("%s Sign in again, then press r: %s", reason, command)
}

func (p *Provider) token(ctx context.Context) (string, error) {
	creds, err := p.credentials(ctx)
	if err != nil {
		return "", err
	}
	if !creds.expired(p.now()) {
		return creds.AccessToken, nil
	}
	return p.renewToken(ctx, "")
}

// renewToken delegates the exchange and persistence to Claude's documented
// non-interactive login flow. A rejected token forces one renewal after a 401.
func (p *Provider) renewToken(ctx context.Context, rejected string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	_, service := p.credentialLocation()
	lockDir := filepath.Join(p.opts.Home, ".config", "husage", "locks")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return "", errors.New("Cannot prepare credential renewal. Check configuration directory permissions.")
	}
	lockPath := filepath.Join(lockDir, service+".lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", errors.New("Credential renewal is locked by another husage process or an interrupted renewal. Retry after it finishes; remove the husage credential lock only if no renewal is running.")
	}
	defer func() { lock.Close(); os.Remove(lockPath) }()
	// Another husage/Claude process may have renewed the token since we read it.
	creds, err := p.credentials(ctx)
	if err != nil {
		return "", err
	}
	if !creds.expired(p.now()) && creds.AccessToken != rejected {
		return creds.AccessToken, nil
	}
	if creds.RefreshToken == "" || len(creds.Scopes) == 0 {
		return "", p.loginError("Claude credentials cannot be renewed automatically (refresh token or scopes missing).")
	}
	renew := p.renewCredentials
	if renew == nil {
		renew = p.renewWithCLI
	}
	if err := renew(ctx, creds); err != nil {
		return "", err
	}
	creds, err = p.credentials(ctx)
	if err != nil {
		return "", err
	}
	if creds.expired(p.now()) || creds.AccessToken == rejected {
		return "", p.loginError("Claude did not save renewed credentials for this profile.")
	}
	return creds.AccessToken, nil
}

func (p *Provider) renewWithCLI(ctx context.Context, creds oauthCredentials) error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return p.loginError("Install Claude CLI to renew this expired login automatically.")
	}
	cmd := exec.CommandContext(ctx, path, "auth", "login", "--claudeai")
	cmd.Env = append(claudecli.Environment(os.Environ(), p.opts.ConfigDir, p.opts.SecureDir),
		"CLAUDE_CODE_OAUTH_REFRESH_TOKEN="+creds.RefreshToken,
		"CLAUDE_CODE_OAUTH_SCOPES="+strings.Join(creds.Scopes, " "))
	// Authentication does not need the user's project, terminal, or prompts.
	cmd.Dir = p.opts.Home
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("Claude credential renewal timed out or was cancelled; retrying on the next reload.")
		}
		// CLI errors may contain tokens/response bodies. Never forward them.
		return p.loginError("Claude could not renew this login. The refresh token may have expired or been revoked, or the service may be unavailable.")
	}
	return nil
}
