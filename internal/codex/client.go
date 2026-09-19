package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/franciscocpg/husage/internal/codexcli"
)

type accountInfo struct {
	Type  string `json:"type"`
	Email string `json:"email"`
	Plan  string `json:"planType"`
}

type reading struct {
	Account *accountInfo
	Limits  rateLimits
}

type rpcClient struct {
	in  *json.Encoder
	out *bufio.Scanner
	id  int
}

func (c *rpcClient) call(method string, params any, result any) error {
	c.id++
	if err := c.in.Encode(map[string]any{"id": c.id, "method": method, "params": params}); err != nil {
		return errors.New("Could not send a request to Codex app-server.")
	}
	for c.out.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(c.out.Bytes(), &msg) != nil {
			return errors.New("Codex returned an invalid protocol message.")
		}
		if msg.Method != "" {
			if len(msg.ID) != 0 {
				// No server-initiated action is needed for account/usage reads.
				if err := c.in.Encode(map[string]any{"id": msg.ID, "error": map[string]any{"code": -32601, "message": "Unsupported by husage"}}); err != nil {
					return errors.New("Could not respond to Codex app-server.")
				}
			}
			continue
		}
		var id int
		if json.Unmarshal(msg.ID, &id) != nil || id != c.id {
			continue
		}
		if msg.Error != nil {
			// Never forward raw error bodies: they can contain authentication data.
			return fmt.Errorf("Codex %s failed (code %d). Check the profile login and network connection.", method, msg.Error.Code)
		}
		if len(msg.Result) == 0 || string(msg.Result) == "null" || json.Unmarshal(msg.Result, result) != nil {
			return errors.New("Codex returned an invalid response.")
		}
		return nil
	}
	return errors.New("Codex app-server stopped or returned an oversized response.")
}

// readUsage uses account endpoints only: it never creates a thread or model turn.
// Native Codex handles file/keychain authentication and token renewal.
func readUsage(ctx context.Context, home string) (reading, error) {
	var out reading
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	path, err := exec.LookPath("codex")
	if err != nil {
		return out, errors.New("Codex CLI was not found in PATH. Install it to read Codex subscriptions.")
	}
	cmd := exec.CommandContext(ctx, path, "app-server")
	cmd.Env = codexcli.Environment(os.Environ(), home)
	cmd.Dir = home
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return out, errors.New("Could not open Codex input.")
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return out, errors.New("Could not open Codex output.")
	}
	defer stdout.Close()
	if err := cmd.Start(); err != nil {
		return out, errors.New("Could not start Codex app-server.")
	}
	defer func() { stdin.Close(); cancel(); cmd.Process.Kill(); cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	rpc := rpcClient{in: json.NewEncoder(stdin), out: scanner}
	var initialized map[string]json.RawMessage
	if err := rpc.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "husage", "title": "husage", "version": "0.1.0"}}, &initialized); err != nil {
		return out, err
	}
	if err := rpc.in.Encode(map[string]string{"method": "initialized"}); err != nil {
		return out, errors.New("Could not initialize Codex app-server.")
	}
	var account struct {
		Account *accountInfo `json:"account"`
	}
	if err := rpc.call("account/read", map[string]bool{"refreshToken": false}, &account); err != nil {
		return out, err
	}
	out.Account = account.Account
	if out.Account == nil || out.Account.Type != "chatgpt" {
		return out, nil
	}
	if err := rpc.call("account/rateLimits/read", nil, &out.Limits); err != nil {
		return out, err
	}
	return out, nil
}
