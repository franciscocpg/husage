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
	var keychain func(context.Context) ([]byte, error)
	if runtime.GOOS == "darwin" {
		keychain = func(ctx context.Context) ([]byte, error) { return readKeychainCredentials(ctx, service) }
	}
	creds, err := readCredentialStores(ctx, keychain, func() ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, ".credentials.json"))
	})
	var loginErr credentialLoginError
	if errors.As(err, &loginErr) {
		return oauthCredentials{}, p.loginError(err.Error())
	}
	return creds, err
}

// These messages contain only our own descriptions, never native command output
// or JSON parse errors, which can include credential values.
type credentialLoginError string

func (e credentialLoginError) Error() string { return string(e) }

func readKeychainCredentials(ctx context.Context, service string) ([]byte, error) {
	data, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", service, "-w").Output()
	if ctx.Err() != nil {
		return nil, credentialContextError(ctx.Err())
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 44 { // errSecItemNotFound
				return nil, os.ErrNotExist
			}
			return nil, fmt.Errorf("Cannot read Claude credentials from macOS Keychain (exit %d). Unlock Keychain and allow access, then retry.", exit.ExitCode())
		}
		return nil, errors.New("Cannot open the macOS Keychain reader for Claude credentials. Retry after checking system access.")
	}
	return data, nil
}

func credentialContextError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("Claude credential lookup timed out. Check Keychain access and retry.")
	}
	return errors.New("Claude credential lookup was cancelled. Retry the refresh.")
}

func readCredentialStores(ctx context.Context, keychain func(context.Context) ([]byte, error), file func() ([]byte, error)) (oauthCredentials, error) {
	if ctx.Err() != nil {
		return oauthCredentials{}, credentialContextError(ctx.Err())
	}
	var keychainErr error
	if keychain != nil {
		data, err := keychain(ctx)
		if ctx.Err() != nil {
			return oauthCredentials{}, credentialContextError(ctx.Err())
		}
		if err == nil && len(data) > 0 {
			return decodeCredentials(data, "macOS Keychain")
		}
		if err == nil {
			keychainErr = credentialLoginError("Saved Claude login in macOS Keychain is empty.")
		} else if !errors.Is(err, os.ErrNotExist) {
			keychainErr = err
		}
	}
	data, err := file()
	if ctx.Err() != nil {
		return oauthCredentials{}, credentialContextError(ctx.Err())
	}
	if err == nil {
		return decodeCredentials(data, "the credential file")
	}
	if errors.Is(err, os.ErrNotExist) {
		if keychainErr != nil {
			return oauthCredentials{}, keychainErr
		}
		return oauthCredentials{}, credentialLoginError("No Claude login found for this profile.")
	}
	if errors.Is(err, os.ErrPermission) {
		return oauthCredentials{}, errors.New("Cannot read the Claude credential file: permission denied. Check file permissions, then retry.")
	}
	return oauthCredentials{}, errors.New("Cannot read the Claude credential file. Check that it is a readable file, then retry.")
}

func decodeCredentials(data []byte, source string) (oauthCredentials, error) {
	var stored struct {
		OAuth *oauthCredentials `json:"claudeAiOauth"`
	}
	if json.Unmarshal(data, &stored) != nil {
		return oauthCredentials{}, credentialLoginError("Saved Claude login in " + source + " contains invalid credential data.")
	}
	if stored.OAuth == nil {
		return oauthCredentials{}, credentialLoginError("Saved Claude login in " + source + " is missing OAuth data.")
	}
	if stored.OAuth.AccessToken == "" && stored.OAuth.RefreshToken == "" {
		return oauthCredentials{}, credentialLoginError("Saved Claude login in " + source + " is incomplete: access and refresh tokens are missing. Automatic renewal is unavailable.")
	}
	return *stored.OAuth, nil
}

const loginWarning = "Login required. Sign in again for this profile, then press r."

type recoveryError struct {
	detail        string
	warning       string
	loginRequired bool
}

func (e recoveryError) Error() string { return e.detail }

func requiresLogin(err error) bool {
	var recovery recoveryError
	if errors.As(err, &recovery) {
		return recovery.loginRequired
	}
	var login credentialLoginError
	return errors.As(err, &login) || errors.Is(err, errUsageUnauthorized)
}

func recoveryWarning(err error) string {
	var recovery recoveryError
	if errors.As(err, &recovery) {
		return recovery.warning
	}
	var login credentialLoginError
	if errors.As(err, &login) || errors.Is(err, errUsageUnauthorized) {
		return loginWarning
	}
	return ""
}

func (p *Provider) loginError(reason string) error {
	err := p.loginRecoveryError(reason, loginWarning)
	err.loginRequired = true
	return err
}

func (p *Provider) loginRecoveryError(reason, warning string) recoveryError {
	command := strings.ReplaceAll(claudecli.LoginCommand(p.opts.ConfigDir, p.opts.SecureDir), " \\\n", " ")
	return recoveryError{detail: fmt.Sprintf("%s Sign in again, then press r: %s", reason, command), warning: warning}
}

func (p *Provider) token(ctx context.Context) (string, error) {
	creds, err := p.credentials(ctx)
	if err != nil {
		return "", err
	}
	if creds.AccessToken != "" && !creds.expired(p.now()) {
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
	if creds.AccessToken != "" && !creds.expired(p.now()) && creds.AccessToken != rejected {
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
	if creds.AccessToken == "" || creds.expired(p.now()) || creds.AccessToken == rejected {
		return "", p.loginError("Claude did not save renewed credentials for this profile.")
	}
	return creds.AccessToken, nil
}

func (p *Provider) renewWithCLI(ctx context.Context, creds oauthCredentials) error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return p.loginRecoveryError("Install Claude CLI to renew this expired login automatically.", "Install Claude CLI to renew this login, then press r.")
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
		return p.loginRecoveryError("Claude could not renew this login. The refresh token may have expired or been revoked, or the service may be unavailable.", "Login renewal failed. Retry with r; if it persists, sign in again.")
	}
	return nil
}
