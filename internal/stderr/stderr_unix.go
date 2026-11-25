//go:build unix

package stderr

import (
	"io"
	"math"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"
)

var (
	// redirects reports whether standard error goes to a file while the
	// dashboard runs: only on macOS, where the processes husage starts write
	// noise.
	redirects = runtime.GOOS == "darwin"

	isTerminal = term.IsTerminal
)

// ToFile points standard error at the file name, when standard error is a
// terminal, on macOS. What husage writes there goes to the file, and so does
// what the processes it starts write before they run their command. A crash
// still shows on the terminal too, but for the line a fatal error starts
// with, such as "fatal error: concurrent map writes", and what a library
// writes before it aborts, which only go to the file. So the file of the
// last run stays, as name with .old before its extension. ToFile returns nil
// when standard error stays as it is.
func ToFile(name string) *Redirect {
	if !redirects || !isTerminal(uintptr(unix.Stderr)) {
		return nil
	}
	// The runtime then waits on standard error through its poller, which
	// loses it once another file takes its place: once back, a write that
	// has to wait would wait forever.
	if flags, err := unix.FcntlInt(uintptr(unix.Stderr), unix.F_GETFL, 0); err != nil || flags&unix.O_NONBLOCK != 0 {
		return nil
	}

	// A new file also keeps the lines of another run of husage, which writes
	// to the old one, out of this run's.
	_ = os.Rename(name, oldName(name))
	file, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND, 0600)
	if err != nil {
		return nil
	}
	// Close-on-exec, so the commands husage starts, such as codex app-server,
	// don't keep the terminal open.
	fd, err := unix.FcntlInt(uintptr(unix.Stderr), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		file.Close()
		return nil
	}
	terminal := os.NewFile(uintptr(fd), "/dev/stderr")
	if err := unix.Dup2(int(file.Fd()), unix.Stderr); err != nil {
		terminal.Close()
		file.Close()
		return nil
	}
	// The runtime writes a crash to standard error, and, as it did before,
	// to the terminal, where it shows over the dashboard.
	_ = debug.SetCrashOutput(terminal, debug.CrashOptions{})
	return &Redirect{terminal: terminal, fd: fd, file: file}
}

// Restore points standard error back at the terminal, and writes there what
// went to the file since Mark, or all of it with all, such as the panic
// husage recovered from, but the noise. Restore does nothing on a nil
// Redirect.
func (r *Redirect) Restore(all bool) {
	if r == nil {
		return
	}
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	_ = unix.Dup2(r.fd, unix.Stderr)
	from := r.mark
	if all {
		from = 0
	}
	// Read from there, not from where the file is at, which the writes left
	// at its end.
	if b, err := io.ReadAll(io.NewSectionReader(r.file, from, math.MaxInt64-from)); err == nil {
		_, _ = r.terminal.Write(withoutNoise(b))
	}
	r.terminal.Close()
	r.file.Close()
}
