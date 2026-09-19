package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A subprocess running this test stands in for the real Codex executable.
func TestAppServerHelper(t *testing.T) {
	if os.Getenv("HUSAGE_CODEX_HELPER") != "1" {
		return
	}
	if os.Getenv("CODEX_HOME") != os.Getenv("HUSAGE_EXPECT_HOME") {
		os.Exit(20)
	}
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN"} {
		if os.Getenv(key) != "" {
			os.Exit(21)
		}
	}
	mode := os.Getenv("HUSAGE_CODEX_MODE")
	decoder := json.NewDecoder(bufio.NewReader(os.Stdin))
	encoder := json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if decoder.Decode(&request) != nil {
			os.Exit(0)
		}
		switch request.Method {
		case "initialize":
			encoder.Encode(map[string]any{"id": request.ID, "result": map[string]string{"userAgent": "fake-codex"}})
		case "initialized":
		case "account/read":
			if mode == "hang" {
				time.Sleep(time.Minute)
				os.Exit(22)
			}
			if string(request.Params) != `{"refreshToken":false}` {
				os.Exit(23)
			}
			encoder.Encode(map[string]any{"method": "account/updated", "params": map[string]string{"authMode": "chatgpt"}})
			var account any = map[string]string{"type": "chatgpt", "email": "test@example.com", "planType": "pro"}
			if mode == "logged-out" {
				account = nil
			}
			if mode == "apikey" {
				account = map[string]string{"type": "apiKey"}
			}
			encoder.Encode(map[string]any{"id": request.ID, "result": map[string]any{"account": account, "requiresOpenaiAuth": true}})
		case "account/rateLimits/read":
			if mode == "error" {
				encoder.Encode(map[string]any{"id": request.ID, "error": map[string]any{"code": -32000, "message": "SECRET_TOKEN must never escape"}})
			} else {
				encoder.Encode(map[string]any{"id": request.ID, "result": json.RawMessage(usageFixture)})
			}
		default:
			fmt.Fprintln(os.Stderr, "unexpected method")
			os.Exit(24)
		}
	}
}

func TestAppServerProtocolAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake executable wrapper")
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	// The real executable path is passed in an environment variable, not shell source.
	if err = os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexec \"$HUSAGE_TEST_BINARY\" -test.run=^TestAppServerHelper$\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("HUSAGE_TEST_BINARY", testBinary)
	t.Setenv("HUSAGE_CODEX_HELPER", "1")
	home := t.TempDir()
	t.Setenv("HUSAGE_EXPECT_HOME", home)
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN"} {
		t.Setenv(key, "other-account-secret")
	}
	for _, mode := range []string{"ok", "error", "logged-out", "apikey", "hang"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HUSAGE_CODEX_MODE", mode)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if mode == "hang" {
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
			}
			start := time.Now()
			r, err := readUsage(ctx, home)
			switch mode {
			case "ok":
				if err != nil || r.Account == nil || r.Account.Email != "test@example.com" || r.Limits.AccountID != "workspace-one" {
					t.Fatal(r.Account, err)
				}
			case "error":
				if err == nil || strings.Contains(err.Error(), "SECRET_TOKEN") {
					t.Fatal("unsafe error", err)
				}
			case "logged-out":
				if err != nil || r.Account != nil {
					t.Fatal(r.Account, err)
				}
			case "apikey":
				if err != nil || r.Account.Type != "apiKey" {
					t.Fatal(r.Account, err)
				}
			case "hang":
				if err == nil || time.Since(start) > 3*time.Second {
					t.Fatal("cancellation failed", err)
				}
			}
		})
	}
	// Ensure tests never needed a real installed Codex or credential file.
	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatal(err)
	}
}
