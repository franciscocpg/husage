// Package claudecli isolates native Claude authentication commands by profile.
package claudecli

import "strings"

var Unset = []string{
	"CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN",
	"CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_OAUTH_SCOPES",
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
}

func Environment(environ []string, dir, secureDir string) []string {
	out := make([]string, 0, len(environ)+2)
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		exclude := key == "CLAUDE_CONFIG_DIR"
		for _, name := range Unset {
			exclude = exclude || key == name
		}
		if !exclude {
			out = append(out, entry)
		}
	}
	if dir != "" {
		out = append(out, "CLAUDE_CONFIG_DIR="+dir)
	}
	if secureDir != "" {
		out = append(out, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+secureDir)
	}
	return out
}

func LoginCommand(dir, secureDir string) string {
	lines := []string{"env", "  -u CLAUDE_CONFIG_DIR"}
	for _, name := range Unset {
		lines = append(lines, "  -u "+name)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	if dir != "" {
		lines = append(lines, "  CLAUDE_CONFIG_DIR="+quote(dir))
	}
	if secureDir != "" {
		lines = append(lines, "  CLAUDE_SECURESTORAGE_CONFIG_DIR="+quote(secureDir))
	}
	lines = append(lines, "  claude auth login --claudeai")
	return strings.Join(lines, " \\\n")
}
