// Package stderr keeps what goes to standard error off the terminal while the
// husage dashboard runs there.
//
// On macOS, once memory runs critically low, the system loads its malloc
// stack logging library into the programs that run, to report on their
// memory, without turning the logging on. From then on, every process such a
// program forks writes
//
//	husage(PID) MallocStackLogging: can't turn off malloc stack logging because it was not enabled.
//
// to the standard error it got from the program, as it starts, before it
// runs the command husage started: hence husage's name, with the process ID
// of the command. The command's own standard error, such as /dev/null, is
// only in place after that, so it can't keep the line away. husage starts
// commands on every refresh, such as security for the Keychain, codex
// app-server and claude, and on the terminal their lines garble the screen
// the dashboard draws.
package stderr

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// noise is what the processes husage starts write on macOS.
const noise = "MallocStackLogging: can't turn off malloc stack logging because it was not enabled."

// Redirect is standard error going to a file instead of the terminal.
type Redirect struct {
	// terminal is the terminal standard error was, and fd its file
	// descriptor.
	terminal *os.File
	fd       int
	file     *os.File
	// mark is where the file ended when Mark was called.
	mark int64
}

// Terminal is where a command husage runs on the terminal, such as a login,
// writes its standard error: the terminal standard error was, which, unlike
// os.Stderr, the redirect leaves on the terminal. Terminal is os.Stderr on a
// nil Redirect.
func (r *Redirect) Terminal() *os.File {
	if r == nil {
		return os.Stderr
	}
	return r.terminal
}

// Mark notes where the file ends, as the dashboard closes: before, what went
// to standard error showed on the dashboard, and went away with it. Mark does
// nothing on a nil Redirect.
func (r *Redirect) Mark() {
	if r == nil {
		return
	}
	if info, err := r.file.Stat(); err == nil {
		r.mark = info.Size()
	}
}

func oldName(name string) string {
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + ".old" + ext
}

// withoutNoise is b without its lines of noise.
func withoutNoise(b []byte) []byte {
	var kept []byte
	for line := range bytes.Lines(b) {
		if !bytes.Contains(line, []byte(noise)) {
			kept = append(kept, line...)
		}
	}
	return kept
}
