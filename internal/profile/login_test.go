package profile

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoginRunsClaudeWithIsolatedCredentialsAndCanRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executable is a POSIX shell script")
	}
	bin := t.TempDir()
	script := `#!/bin/sh
[ "$#" = 3 ] && [ "$1" = auth ] && [ "$2" = login ] && [ "$3" = --claudeai ] || exit 40
[ -d "$CLAUDE_CONFIG_DIR" ] || exit 41
[ -z "${CLAUDE_SECURESTORAGE_CONFIG_DIR+x}" ] || exit 42
[ -z "${CLAUDE_CODE_OAUTH_TOKEN+x}" ] || exit 43
[ -z "${ANTHROPIC_API_KEY+x}" ] || exit 44
[ -z "${ANTHROPIC_AUTH_TOKEN+x}" ] || exit 45
[ -z "${CLAUDE_CODE_OAUTH_REFRESH_TOKEN+x}" ] || exit 46
[ -z "${CLAUDE_CODE_OAUTH_SCOPES+x}" ] || exit 47
printf '%s' "$CLAUDE_CONFIG_DIR" > "$HUSAGE_LOGIN_RECEIPT"
printf 'fake login output'
printf '{"installMethod":"native"}' > "$CLAUDE_CONFIG_DIR/.claude.json"
exit "$HUSAGE_LOGIN_EXIT"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, key := range append(loginUnset, "CLAUDE_CONFIG_DIR") {
		t.Setenv(key, "unrelated-current-login")
	}
	receipt := filepath.Join(t.TempDir(), "receipt")
	t.Setenv("HUSAGE_LOGIN_RECEIPT", receipt)
	t.Setenv("HUSAGE_LOGIN_EXIT", "1")
	s := Store{Home: filepath.Join(t.TempDir(), "home with ' quote")}
	login := NewLogin(context.Background(), s, "work")
	var output bytes.Buffer
	login.SetStdin(strings.NewReader(""))
	login.SetStdout(&output)
	login.SetStderr(&output)
	if _, err := os.Stat(s.Directory("work")); !os.IsNotExist(err) {
		t.Fatal("construction created directory")
	}
	if err := login.Run(); err == nil {
		t.Fatal("failed login reported success")
	}
	if _, err := os.Stat(s.ConfigPath()); !os.IsNotExist(err) {
		t.Fatal("failed login registered a profile")
	}
	t.Setenv("HUSAGE_LOGIN_EXIT", "0")
	if err := login.Run(); err != nil {
		t.Fatal("retry failed", err)
	}
	// Closing the form or restarting the app creates a new runner. Setup files
	// from the previous attempt must not force the user to pick a new name.
	if err := NewLogin(context.Background(), s, "work").Run(); err != nil {
		t.Fatal("retry with a fresh runner failed", err)
	}
	b, _ := os.ReadFile(receipt)
	if string(b) != s.Directory("work") {
		t.Fatal("wrong credential directory", string(b))
	}
	if _, err := os.Stat(s.ConfigPath()); !os.IsNotExist(err) {
		t.Fatal("runner registered before success callback")
	}
	if !strings.Contains(output.String(), "fake login output") {
		t.Fatal("interactive output not forwarded")
	}
	if os.Getenv("CLAUDE_CONFIG_DIR") != "unrelated-current-login" {
		t.Fatal("changed parent's environment")
	}
}

func TestMissingClaudeDoesNotPrepareDirectory(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := Store{Home: t.TempDir()}
	if err := NewLogin(context.Background(), s, "work").Run(); err == nil {
		t.Fatal("missing executable accepted")
	}
	entries, _ := os.ReadDir(s.Home)
	if len(entries) != 0 {
		t.Fatal("missing executable created profile files")
	}
}

func TestLoginPreviewMatchesEnvironmentAndQuotesPaths(t *testing.T) {
	dir := "/home/it's my/profile"
	command := LoginCommand(dir)
	if !strings.Contains(command, `'"'"'`) || !strings.Contains(command, "claude auth login --claudeai") {
		t.Fatal(command)
	}
	for _, key := range loginUnset {
		if !strings.Contains(command, "-u "+key) {
			t.Fatal("preview omitted unset", key)
		}
	}
	env := loginEnvironment([]string{"KEEP=yes", "CLAUDE_CONFIG_DIR=old", "ANTHROPIC_API_KEY=secret"}, dir)
	if len(env) != 2 || env[0] != "KEEP=yes" || env[1] != "CLAUDE_CONFIG_DIR="+dir {
		t.Fatal("wrong environment")
	}
}
