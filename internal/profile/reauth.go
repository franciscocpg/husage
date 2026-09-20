package profile

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/franciscocpg/husage/internal/claudecli"
	"github.com/franciscocpg/husage/internal/codexcli"
	"github.com/franciscocpg/husage/internal/subscription"
)

// ExistingLogin uses the native profile in place. It never prepares a new
// directory, registers a profile, or deletes existing credentials.
type ExistingLogin struct {
	ctx            context.Context
	target         subscription.LoginTarget
	stdin          io.Reader
	stdout, stderr io.Writer
}

func NewExistingLogin(ctx context.Context, target subscription.LoginTarget) *ExistingLogin {
	return &ExistingLogin{ctx: ctx, target: target}
}
func (l *ExistingLogin) SetStdin(r io.Reader)  { l.stdin = r }
func (l *ExistingLogin) SetStdout(w io.Writer) { l.stdout = w }
func (l *ExistingLogin) SetStderr(w io.Writer) { l.stderr = w }

func ExistingLoginCommand(t subscription.LoginTarget) string {
	switch t.Provider {
	case "claude":
		return claudecli.LoginCommand(t.Directory, t.SecureDirectory)
	case "codex":
		return codexcli.LoginCommand(t.Directory)
	case "cursor":
		return "env -u CURSOR_API_KEY cursor-agent login"
	default:
		return ""
	}
}

func (l *ExistingLogin) Run() error {
	if err := l.ctx.Err(); err != nil {
		return err
	}
	var executable string
	var args, env []string
	switch l.target.Provider {
	case "claude":
		executable, args = "claude", []string{"auth", "login", "--claudeai"}
		env = claudecli.Environment(os.Environ(), l.target.Directory, l.target.SecureDirectory)
	case "codex":
		if l.target.Directory == "" {
			return fmt.Errorf("Codex profile directory is unavailable. Close this screen and select it again.")
		}
		executable, args = "codex", []string{"login"}
		env = codexcli.Environment(os.Environ(), l.target.Directory)
	case "cursor":
		executable, args = "cursor-agent", []string{"login"}
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "CURSOR_API_KEY=") {
				env = append(env, entry)
			}
		}
	default:
		return fmt.Errorf("Login is not supported for this provider.")
	}
	path, err := exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("%s was not found in PATH. Install it, then press Enter to retry.", executable)
	}
	for _, dir := range []string{l.target.Directory, l.target.SecureDirectory} {
		if dir == "" {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("The existing profile directory is unavailable. Restore it, then retry.")
		}
	}
	cmd := exec.CommandContext(l.ctx, path, args...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, l.stdin, l.stdout, l.stderr
	if cmd.Run() != nil {
		// Do not include native error output, which can contain authentication data.
		return fmt.Errorf("Login failed or was cancelled. Press Enter to retry, or Esc to return to the dashboard.")
	}
	return nil
}
