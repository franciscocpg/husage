package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/franciscocpg/husage/internal/subscription"
)

func TestDemoJSONDoesNotNeedLocalCredentials(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var out bytes.Buffer
	if err := run([]string{"--demo", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var accounts []subscription.Account
	if err := json.Unmarshal(out.Bytes(), &accounts); err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 || accounts[0].Windows[0].Used != 38 {
		t.Fatal(accounts)
	}
	if strings.Contains(out.String(), "accessToken") {
		t.Fatal("unexpected credential field")
	}
}

func TestInvalidOptions(t *testing.T) {
	for _, args := range [][]string{{"--refresh", "1s"}, {"--width", "0"}, {"--timezone", "Invalid/Zone"}, {"unexpected"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestVersionDoesNotReadAccountConfiguration(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"--version", "--profiles", "/missing/profiles.json", "--timezone", "Invalid/Zone"}, &out); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "husage "+version+" (commit "+commit+")\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

type execCommand struct{ stderr io.Writer }

func (*execCommand) Run() error              { return nil }
func (*execCommand) SetStdin(io.Reader)      {}
func (*execCommand) SetStdout(io.Writer)     {}
func (c *execCommand) SetStderr(w io.Writer) { c.stderr = w }

// Bubble Tea gives a login os.Stderr, which goes to stderr.log while the
// dashboard runs on macOS: the login gets the terminal instead.
func TestLoginStderrStaysOnTerminal(t *testing.T) {
	login := &execCommand{}
	onTerminal(login, nil).SetStderr(&bytes.Buffer{})
	if login.stderr != os.Stderr {
		t.Errorf("login stderr = %v, want the terminal, os.Stderr without a redirect", login.stderr)
	}
}
