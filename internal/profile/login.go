package profile

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/franciscocpg/husage/internal/claudecli"
	"github.com/franciscocpg/husage/internal/codexcli"
)

// Login implements Bubble Tea's ExecCommand without coupling storage to the UI.
// Constructing it has no side effects. Run is invoked only after confirmation.
type Login struct {
	ctx             context.Context
	store           Store
	name, directory string
	stdin           io.Reader
	stdout, stderr  io.Writer
}

func NewLogin(ctx context.Context, store Store, name string) *Login {
	return &Login{ctx: ctx, store: store, name: name}
}
func (l *Login) SetStdin(r io.Reader)  { l.stdin = r }
func (l *Login) SetStdout(w io.Writer) { l.stdout = w }
func (l *Login) SetStderr(w io.Writer) { l.stderr = w }

func (l *Login) Run() error {
	if err := l.ctx.Err(); err != nil {
		return err
	}
	name := "Claude"
	executable := "claude"
	args := []string{"auth", "login", "--claudeai"}
	if l.store.Kind() == "codex" {
		name = "Codex"
		executable = "codex"
		args = []string{"login"}
	}
	path, err := exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("%s CLI was not found in PATH. Install it, then retry.", name)
	}
	if l.directory == "" {
		l.directory, err = l.store.Prepare(l.ctx, l.name)
		if err != nil {
			return err
		}
	}
	info, err := os.Lstat(l.directory)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("The prepared login directory is no longer available.")
	}
	cmd := exec.CommandContext(l.ctx, path, args...)
	cmd.Env = loginEnvironment(os.Environ(), l.directory)
	if l.store.Kind() == "codex" {
		cmd.Env = codexcli.Environment(os.Environ(), l.directory)
	}
	cmd.Stdin = l.stdin
	cmd.Stdout = l.stdout
	cmd.Stderr = l.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s login failed or was cancelled (%v). Profile not added; press Enter to retry.", name, err)
	}
	return nil
}

var loginUnset = claudecli.Unset

func loginEnvironment(environ []string, dir string) []string {
	return claudecli.Environment(environ, dir, "")
}

// LoginCommand is the shell-readable equivalent of Run. Run itself never uses a
// shell; executable arguments and environment variables are passed separately.
func LoginCommand(dir string) string {
	return claudecli.LoginCommand(dir, "")
}
