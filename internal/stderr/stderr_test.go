//go:build unix

package stderr

import (
	"cmp"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

const crashEnv = "HUSAGE_STDERR_TEST_CRASH"

// realStderr is standard error as the test program started: go test -json
// points os.Stderr at standard output once the tests start.
var realStderr = os.Stderr

// fakeTerminal points standard error at a file the test reads as the
// terminal, until the test ends, and has ToFile take it for a terminal, on
// any system. A crash or a timeout meanwhile writes to that file, in the
// test's temporary folder, not to the test's output.
func fakeTerminal(t *testing.T) string {
	t.Helper()

	name := filepath.Join(t.TempDir(), "terminal")
	saved, err := unix.FcntlInt(uintptr(unix.Stderr), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldRedirects, oldIsTerminal := redirects, isTerminal
	t.Cleanup(func() {
		_ = unix.Dup2(saved, unix.Stderr)
		_ = unix.Close(saved)
		redirects, isTerminal = oldRedirects, oldIsTerminal
	})
	pointStderrAt(t, name)
	takeForTerminal()
	return name
}

// takeForTerminal has ToFile take standard error, and only it, for a
// terminal, on any system.
func takeForTerminal() {
	redirects = true
	isTerminal = func(fd uintptr) bool { return fd == uintptr(unix.Stderr) }
}

func pointStderrAt(t *testing.T, name string) {
	t.Helper()

	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Dup2(int(f.Fd()), unix.Stderr); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, name string) string {
	t.Helper()

	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// write writes s to standard error as the processes husage starts do
// before they run their command: to file descriptor 2, not through
// os.Stderr.
func write(t *testing.T, s string) {
	t.Helper()

	if _, err := unix.Write(unix.Stderr, []byte(s)); err != nil {
		t.Fatal(err)
	}
}

func TestToFile(t *testing.T) {
	terminal := fakeTerminal(t)
	name := filepath.Join(t.TempDir(), "stderr.log")

	r := ToFile(name)
	if r == nil {
		t.Fatal("ToFile returned nil, want a redirect")
	}
	write(t, "forked\n")
	// A command that writes to the standard error it got, as the processes
	// husage starts do as they start.
	cmd := exec.Command("sh", "-c", "echo command >&2")
	cmd.Stderr = realStderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got, want := read(t, terminal), ""; got != want {
		t.Errorf("terminal = %q while redirected, want %q", got, want)
	}

	r.Mark()
	r.Restore(false)
	write(t, "after\n")
	if got, want := read(t, name), "forked\ncommand\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if got, want := read(t, terminal), "after\n"; got != want {
		t.Errorf("terminal = %q, want %q", got, want)
	}
}

func TestToFileKeepsLastRun(t *testing.T) {
	fakeTerminal(t)
	dir := t.TempDir()
	name := filepath.Join(dir, "stderr.log")
	if err := os.WriteFile(name, []byte("fatal error: last run\n"), 0600); err != nil {
		t.Fatal(err)
	}

	r := ToFile(name)
	if r == nil {
		t.Fatal("ToFile returned nil, want a redirect")
	}
	write(t, "this run\n")
	r.Restore(false)

	if got, want := read(t, name), "this run\n"; got != want {
		t.Errorf("stderr.log = %q, want %q", got, want)
	}
	if got, want := read(t, filepath.Join(dir, "stderr.old.log")), "fatal error: last run\n"; got != want {
		t.Errorf("stderr.old.log = %q, want %q", got, want)
	}
}

func TestRestore(t *testing.T) {
	const noiseLine = "husage(31961) " + noise + "\n"
	tests := []struct {
		name string
		all  bool
		want string
	}{
		{
			name: "since mark",
			want: "Caught panic:\n",
		},
		{
			name: "all",
			all:  true,
			want: "before mark\nCaught panic:\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terminal := fakeTerminal(t)

			r := ToFile(filepath.Join(t.TempDir(), "stderr.log"))
			if r == nil {
				t.Fatal("ToFile returned nil, want a redirect")
			}
			write(t, "before mark\n"+noiseLine)
			r.Mark()
			write(t, noiseLine+"Caught panic:\n"+noiseLine)
			r.Restore(tt.all)
			write(t, "program experienced a panic\n")

			if got, want := read(t, terminal), tt.want+"program experienced a panic\n"; got != want {
				t.Errorf("terminal = %q, want %q", got, want)
			}
		})
	}
}

func TestToFileLeavesStderr(t *testing.T) {
	tests := []struct {
		name        string
		redirects   bool
		notTerminal bool
		nonBlocking bool
		file        string
	}{
		{name: "not on macOS"},
		{name: "not a terminal", redirects: true, notTerminal: true},
		{name: "non-blocking terminal", redirects: true, nonBlocking: true},
		{name: "file can't be created", redirects: true, file: "missing/stderr.log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terminal := fakeTerminal(t)
			redirects = tt.redirects
			if tt.notTerminal {
				isTerminal = func(uintptr) bool { return false }
			}
			if tt.nonBlocking {
				if err := unix.SetNonblock(unix.Stderr, true); err != nil {
					t.Fatal(err)
				}
			}
			file := cmp.Or(tt.file, "stderr.log")
			name := filepath.Join(t.TempDir(), file)

			r := ToFile(name)
			if r != nil {
				r.Restore(false)
				t.Fatal("ToFile returned a redirect, want nil")
			}
			write(t, "line\n")
			r.Mark()
			r.Restore(true)

			if got, want := read(t, terminal), "line\n"; got != want {
				t.Errorf("terminal = %q, want %q", got, want)
			}
			if _, err := os.Stat(name); err == nil {
				t.Errorf("ToFile created %s", file)
			}
		})
	}
}

// The commands husage starts don't get the terminal ToFile keeps: a command
// that outlives husage would keep it open.
func TestTerminalCloseOnExec(t *testing.T) {
	fakeTerminal(t)

	r := ToFile(filepath.Join(t.TempDir(), "stderr.log"))
	if r == nil {
		t.Fatal("ToFile returned nil, want a redirect")
	}
	defer r.Restore(false)
	flags, err := unix.FcntlInt(uintptr(r.fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Error("the terminal isn't close-on-exec")
	}
}

// A login husage runs on the terminal writes its standard error there, while
// standard error goes to the file.
func TestTerminal(t *testing.T) {
	terminal := fakeTerminal(t)
	name := filepath.Join(t.TempDir(), "stderr.log")

	r := ToFile(name)
	if r == nil {
		t.Fatal("ToFile returned nil, want a redirect")
	}
	write(t, "forked\n")
	cmd := exec.Command("sh", "-c", "echo login >&2")
	cmd.Stderr = r.Terminal()
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got, want := read(t, terminal), "login\n"; got != want {
		t.Errorf("terminal = %q while redirected, want %q", got, want)
	}

	r.Mark()
	r.Restore(false)
	if got, want := read(t, name), "forked\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestTerminalWithoutRedirect(t *testing.T) {
	var r *Redirect
	if got := r.Terminal(); got != os.Stderr {
		t.Errorf("Terminal() = %v on a nil Redirect, want os.Stderr", got)
	}
}

// A crash shows on the terminal, once, while standard error goes to the
// file, as before, and in the file. Once standard error is back, it shows on
// the terminal once.
func TestCrash(t *testing.T) {
	if mode, dir, ok := strings.Cut(os.Getenv(crashEnv), ":"); ok {
		crash(t, mode, dir)
		return
	}

	tests := []struct {
		mode   string
		logged bool
	}{
		{mode: "redirected", logged: true},
		{mode: "restored"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrash$")
			cmd.Env = append(os.Environ(), crashEnv+"="+tt.mode+":"+dir)
			out, err := cmd.CombinedOutput()
			if exitErr := (*exec.ExitError)(nil); !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("the test program ended with %v, want exit status 2 from a crash; it wrote:\n%s", err, out)
			}

			if got := strings.Count(read(t, filepath.Join(dir, "terminal")), "panic: boom"); got != 1 {
				t.Errorf("the terminal shows the crash %d times, want once", got)
			}
			logged := strings.Contains(read(t, filepath.Join(dir, "stderr.log")), "panic: boom")
			if logged != tt.logged {
				t.Errorf("crash in stderr.log = %v, want %v", logged, tt.logged)
			}
		})
	}
}

// crash crashes the test program, with standard error redirected, or once
// it is back with mode "restored". The terminal and stderr.log are in dir.
func crash(t *testing.T, mode, dir string) {
	pointStderrAt(t, filepath.Join(dir, "terminal"))
	takeForTerminal()
	r := ToFile(filepath.Join(dir, "stderr.log"))
	if r == nil {
		t.Fatal("ToFile returned nil, want a redirect")
	}
	if mode == "restored" {
		r.Restore(false)
	}
	// The test goroutine waits for good: were it to end, the test program
	// could exit before the runtime wrote the crash.
	go panic("boom")
	select {}
}
