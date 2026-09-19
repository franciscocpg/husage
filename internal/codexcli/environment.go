// Package codexcli isolates native Codex commands by CODEX_HOME.
package codexcli

import "strings"

var Unset = []string{"CODEX_HOME", "OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN"}

func Environment(environ []string, home string) []string {
	var out []string
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		skip := false
		for _, excluded := range Unset {
			skip = skip || key == excluded
		}
		if !skip {
			out = append(out, entry)
		}
	}
	return append(out, "CODEX_HOME="+home)
}

func LoginCommand(home string) string {
	parts := []string{"env"}
	for _, key := range Unset {
		parts = append(parts, "  -u "+key)
	}
	parts = append(parts, "  CODEX_HOME='"+strings.ReplaceAll(home, "'", "'\"'\"'")+"'", "  codex login")
	return strings.Join(parts, " \\\n")
}
