package profile

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
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
	path, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("Claude CLI was not found in PATH. Install it, then retry.")
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
	cmd := exec.CommandContext(l.ctx, path, "auth", "login", "--claudeai")
	cmd.Env = loginEnvironment(os.Environ(), l.directory)
	cmd.Stdin = l.stdin
	cmd.Stdout = l.stdout
	cmd.Stderr = l.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Claude login failed or was cancelled (%v). Profile not added; press Enter to retry.", err)
	}
	return nil
}

var loginUnset = []string{"CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

func loginEnvironment(environ []string, dir string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		exclude := key == "CLAUDE_CONFIG_DIR"
		for _, name := range loginUnset {
			exclude = exclude || key == name
		}
		if !exclude {
			out = append(out, entry)
		}
	}
	return append(out, "CLAUDE_CONFIG_DIR="+dir)
}

// LoginCommand is the shell-readable equivalent of Run. Run itself never uses a
// shell; executable arguments and environment variables are passed separately.
func LoginCommand(dir string) string {
	lines := []string{"env"}
	for _, name := range loginUnset {
		lines = append(lines, "  -u "+name)
	}
	quoted := "'" + strings.ReplaceAll(dir, "'", "'\"'\"'") + "'"
	lines = append(lines, "  CLAUDE_CONFIG_DIR="+quoted, "  claude auth login --claudeai")
	return strings.Join(lines, " \\\n")
}
