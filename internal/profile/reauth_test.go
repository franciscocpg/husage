package profile

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/franciscocpg/husage/internal/subscription"
)

func TestExistingLoginIsIsolatedAndDoesNotRegisterOrDelete(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executables")
	}
	bin, home, dir, secure := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_OAUTH_SCOPES", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CODEX_HOME", "OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "CURSOR_API_KEY"} {
		t.Setenv(key, "unrelated-secret")
	}
	t.Setenv("HUSAGE_EXPECT_DIR", dir)
	t.Setenv("HUSAGE_EXPECT_SECURE", secure)
	scripts := map[string]string{
		"claude": `[ "$#" = 3 ] && [ "$1" = auth ] && [ "$2" = login ] && [ "$3" = --claudeai ] || exit 40
[ "$CLAUDE_CONFIG_DIR" = "$HUSAGE_EXPECT_DIR" ] && [ "$CLAUDE_SECURESTORAGE_CONFIG_DIR" = "$HUSAGE_EXPECT_SECURE" ] || exit 41
[ -z "${CLAUDE_CODE_OAUTH_TOKEN+x}" ] && [ -z "${CLAUDE_CODE_OAUTH_REFRESH_TOKEN+x}" ] && [ -z "${CLAUDE_CODE_OAUTH_SCOPES+x}" ] && [ -z "${ANTHROPIC_API_KEY+x}" ] && [ -z "${ANTHROPIC_AUTH_TOKEN+x}" ] || exit 42`,
		"codex": `[ "$#" = 1 ] && [ "$1" = login ] && [ "$CODEX_HOME" = "$HUSAGE_EXPECT_DIR" ] || exit 40
[ -z "${OPENAI_API_KEY+x}" ] && [ -z "${CODEX_API_KEY+x}" ] && [ -z "${CODEX_ACCESS_TOKEN+x}" ] || exit 42`,
		"cursor-agent": `[ "$#" = 1 ] && [ "$1" = login ] && [ -z "${CURSOR_API_KEY+x}" ] || exit 40`,
	}
	for name, script := range scripts {
		script = "#!/bin/sh\n" + script + "\nprintf 'interactive native login'\nexit \"$HUSAGE_LOGIN_EXIT\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(marker, []byte("preserved credentials"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []subscription.LoginTarget{{Provider: "claude", Directory: dir, SecureDirectory: secure}, {Provider: "codex", Directory: dir}, {Provider: "cursor"}} {
		login := NewExistingLogin(context.Background(), target)
		var output bytes.Buffer
		login.SetStdin(strings.NewReader(""))
		login.SetStdout(&output)
		login.SetStderr(&output)
		t.Setenv("HUSAGE_LOGIN_EXIT", "1")
		if err := login.Run(); err == nil || strings.Contains(err.Error(), "unrelated-secret") || strings.Contains(err.Error(), "interactive native login") {
			t.Fatal("failed login accepted or leaked native output", err)
		}
		t.Setenv("HUSAGE_LOGIN_EXIT", "0")
		if err := login.Run(); err != nil {
			t.Fatal(target.Provider, err)
		}
		if !strings.Contains(output.String(), "interactive native login") {
			t.Fatal("terminal not forwarded")
		}
		b, err := os.ReadFile(marker)
		if err != nil || string(b) != "preserved credentials" {
			t.Fatal("modified existing credentials")
		}
		if entries, _ := os.ReadDir(home); len(entries) != 0 {
			t.Fatal("registered a profile or created native files")
		}
	}
}

func TestExistingLoginRejectsMissingDirectoryWithoutCreatingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	dir := filepath.Join(t.TempDir(), "missing")
	login := NewExistingLogin(context.Background(), subscription.LoginTarget{Provider: "codex", Directory: dir})
	if login.Run() == nil {
		t.Fatal("missing profile accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("created profile directory")
	}
}
