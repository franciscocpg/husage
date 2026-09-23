package claude

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

type DebugLog struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func NewDebugLog(w io.Writer) *DebugLog { return &DebugLog{w: w, now: time.Now} }

func (d *DebugLog) printf(profile string, secrets []string, format string, args ...any) {
	if d == nil {
		return
	}
	message := redact(fmt.Sprintf(format, args...), secrets...)
	message = strings.ReplaceAll(strings.TrimRight(message, "\n"), "\n", "\n    ")
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprintf(d.w, "%s claude profile=%s: %s\n", d.now().Format(time.RFC3339), profile, message)
}

const redacted = "[REDACTED]"

var (
	ansiPattern      = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	bearerPattern    = regexp.MustCompile(`(?i)(bearer\s+)[^\s"',]+`)
	fieldPattern     = regexp.MustCompile(`(?i)("?\b(?:[a-z_]*(?:token|secret|password|api_?key)|authorization|code)"?\s*[:=]\s*)("[^"]*"|[^\s,}&]+)`)
	anthropicPattern = regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]+`)
	opaquePattern    = regexp.MustCompile(`[A-Za-z0-9_.\-+=]{40,}`)
)

func redact(s string, secrets ...string) string {
	for _, secret := range secrets {
		if len(secret) >= 8 {
			s = strings.ReplaceAll(s, secret, redacted)
		}
	}
	s = ansiPattern.ReplaceAllString(s, "")
	s = bearerPattern.ReplaceAllString(s, "${1}"+redacted)
	s = fieldPattern.ReplaceAllString(s, "${1}"+redacted)
	s = anthropicPattern.ReplaceAllString(s, redacted)
	return opaquePattern.ReplaceAllString(s, redacted)
}

type cappedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.limit - len(b.data); n > room {
		b.truncated = true
		p = p[:max(0, room)]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func (b *cappedBuffer) String() string {
	s := string(b.data)
	if b.truncated {
		if i := strings.LastIndexByte(s, '\n'); i >= 0 {
			s = s[:i]
		} else {
			s = ""
		}
		s += "\n[output truncated]"
	}
	return strings.TrimSpace(s)
}

func describeCredentials(c oauthCredentials) string {
	expires := "none"
	if c.ExpiresAt > 0 {
		expires = time.UnixMilli(c.ExpiresAt).Format(time.RFC3339)
	}
	return fmt.Sprintf("access=%s refresh=%s expires=%s scopes=%q",
		presence(c.AccessToken), presence(c.RefreshToken), expires, strings.Join(c.Scopes, " "))
}

func presence(s string) string {
	if s == "" {
		return "missing"
	}
	return "present"
}
