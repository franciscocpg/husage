package claudecli

import (
	"strings"
	"testing"
)

func TestDefaultLoginClearsInheritedProfileAndCredentials(t *testing.T) {
	env := []string{"KEEP=yes", "CLAUDE_CONFIG_DIR=another-profile"}
	for _, key := range Unset {
		env = append(env, key+"=another-profile-secret")
	}
	clean := Environment(env, "", "")
	if len(clean) != 1 || clean[0] != "KEEP=yes" {
		t.Fatal("inherited authentication affected default login")
	}
	command := LoginCommand("", "")
	for _, key := range append([]string{"CLAUDE_CONFIG_DIR"}, Unset...) {
		if !strings.Contains(command, "-u "+key) {
			t.Fatal("preview omitted environment isolation", key)
		}
	}
}
