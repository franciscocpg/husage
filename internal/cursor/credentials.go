// Package cursor reads the signed-in Cursor CLI account and subscription usage.
package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var errNoLogin = errors.New("No Cursor CLI login found. Run cursor-agent login, then press r.")

// NativeDirectory identifies the CLI's file credential location, not its
// CURSOR_CONFIG_DIR settings override (which does not isolate credentials).
func NativeDirectory(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, ".cursor")
	case "windows":
		root := os.Getenv("APPDATA")
		if root == "" {
			root = filepath.Join(home, "AppData", "Roaming")
		}
		return filepath.Join(root, "Cursor")
	default:
		root := os.Getenv("XDG_CONFIG_HOME")
		if root == "" {
			root = filepath.Join(home, ".config")
		}
		return filepath.Join(root, "cursor")
	}
}

func readToken(ctx context.Context, directory string) (string, error) {
	// Match the native CLI's selected store. A missing Keychain item must not
	// silently resurrect an old file login after the user logs out of Keychain.
	store := os.Getenv("AGENT_CLI_CREDENTIAL_STORE")
	if store == "memory" {
		return "", errNoLogin
	}
	if runtime.GOOS == "darwin" && store != "file" {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		data, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", "cursor-access-token", "-a", "cursor-user", "-w").Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 44 {
				return "", errNoLogin
			}
			return "", errors.New("Cannot read Cursor's Keychain login. Unlock Keychain and retry.")
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			return "", errNoLogin
		}
		return token, nil
	}
	return readTokenFile(filepath.Join(directory, "auth.json"))
}

func readTokenFile(path string) (string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", errNoLogin
	}
	if err != nil {
		return "", errors.New("Cannot read Cursor CLI credentials.")
	}
	defer f.Close()
	var auth struct {
		AccessToken string `json:"accessToken"`
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&auth) != nil {
		return "", errors.New("Cursor CLI credentials are invalid. Run cursor-agent login again.")
	}
	if auth.AccessToken == "" {
		return "", errNoLogin
	}
	return auth.AccessToken, nil
}
