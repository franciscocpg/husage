package main

import (
	"bytes"
	"encoding/json"
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
