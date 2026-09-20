package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer; Windows uses release ZIPs")
	}
	cases := []struct {
		name, system, arch, mode string
		args                     []string
		failure                  bool
	}{
		{name: "latest linux amd64", system: "Linux", arch: "x86_64"},
		{name: "pinned linux arm64", system: "Linux", arch: "aarch64", args: []string{"--version", "0.1.0"}},
		{name: "macOS arm64", system: "Darwin", arch: "arm64"},
		{name: "macOS intel", system: "Darwin", arch: "x86_64"},
		{name: "mismatch", system: "Linux", arch: "x86_64", mode: "mismatch", failure: true},
		{name: "missing checksum", system: "Linux", arch: "x86_64", mode: "missing", failure: true},
		{name: "duplicate checksum", system: "Linux", arch: "x86_64", mode: "duplicate", failure: true},
		{name: "failed download", system: "Linux", arch: "x86_64", mode: "download", failure: true},
		{name: "missing binary", system: "Linux", arch: "x86_64", mode: "missing-binary", failure: true},
		{name: "linked binary", system: "Linux", arch: "x86_64", mode: "linked-binary", failure: true},
		{name: "linked destination", system: "Linux", arch: "x86_64", mode: "linked-destination", failure: true},
		{name: "unsupported system", system: "FreeBSD", arch: "x86_64", failure: true},
		{name: "unsupported arch", system: "Linux", arch: "riscv64", failure: true},
		{name: "invalid version", system: "Linux", arch: "x86_64", args: []string{"--version", "../../other"}, failure: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.system == "Darwin" && runtime.GOOS != "darwin" {
				t.Skip("requires native macOS xattr")
			}
			root := t.TempDir()
			tools := filepath.Join(root, "tools")
			destination := filepath.Join(root, "install with spaces")
			temporary := filepath.Join(root, "temporary")
			for _, dir := range []string{tools, destination, temporary} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, content string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), mode); err != nil {
					t.Fatal(err)
				}
			}
			original := filepath.Join(destination, "husage")
			if tc.mode == "linked-destination" {
				target := filepath.Join(root, "untouched")
				write(target, "old binary", 0755)
				if err := os.Symlink(target, original); err != nil {
					t.Fatal(err)
				}
			} else {
				write(original, "old binary", 0755)
			}
			write(filepath.Join(tools, "uname"), "#!/bin/sh\ncase $1 in -s) printf '%s\\n' \"$INSTALL_TEST_OS\";; -m) printf '%s\\n' \"$INSTALL_TEST_ARCH\";; esac\n", 0755)
			write(filepath.Join(tools, "curl"), `#!/bin/sh
set -eu
output=''
while [ "$#" -gt 0 ]; do
 case "$1" in
  --output) output=$2; shift 2 ;;
  https://*) url=$1; shift ;;
  *) shift ;;
 esac
done
printf '%s\n' "$url" >> "$INSTALL_TEST_ROOT/requests"
case "$url" in
 */releases/latest) printf 'https://github.com/franciscocpg/husage/releases/tag/v0.1.0' ;;
 */checksums.txt) cp "$INSTALL_TEST_ROOT/checksums" "$output" ;;
 *.tar.gz)
  [ "$INSTALL_TEST_MODE" != download ] || exit 22
  cp "$INSTALL_TEST_ROOT/archive.tar.gz" "$output" ;;
 *) exit 22 ;;
esac
`, 0755)
			payload := []byte("#!/bin/sh\nprintf 'new binary\\n'\n")
			f, err := os.Create(filepath.Join(root, "archive.tar.gz"))
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			header := &tar.Header{Name: "husage", Mode: 0755, Size: int64(len(payload)), Typeflag: tar.TypeReg}
			if tc.mode == "missing-binary" {
				header.Name = "other"
			}
			if tc.mode == "linked-binary" {
				header.Typeflag = tar.TypeSymlink
				header.Linkname = original
				header.Size = 0
			}
			if err := tw.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if header.Typeflag == tar.TypeReg {
				if _, err := tw.Write(payload); err != nil {
					t.Fatal(err)
				}
			}
			for _, close := range []func() error{tw.Close, gz.Close, f.Close} {
				if err := close(); err != nil {
					t.Fatal(err)
				}
			}
			data, _ := os.ReadFile(filepath.Join(root, "archive.tar.gz"))
			sum := fmt.Sprintf("%x", sha256.Sum256(data))
			if tc.mode == "mismatch" {
				sum = strings.Repeat("0", 64)
			}
			osName := "linux"
			if tc.system == "Darwin" {
				osName = "darwin"
			}
			arch := "amd64"
			if tc.arch == "arm64" || tc.arch == "aarch64" {
				arch = "arm64"
			}
			asset := fmt.Sprintf("husage_0.1.0_%s_%s.tar.gz", osName, arch)
			checksum := sum + "  " + asset + "\n"
			if tc.mode == "missing" {
				checksum = sum + "  other.tar.gz\n"
			}
			if tc.mode == "duplicate" {
				checksum += checksum
			}
			write(filepath.Join(root, "checksums"), checksum, 0600)
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TMPDIR", temporary)
			t.Setenv("INSTALL_TEST_ROOT", root)
			t.Setenv("INSTALL_TEST_OS", tc.system)
			t.Setenv("INSTALL_TEST_ARCH", tc.arch)
			t.Setenv("INSTALL_TEST_MODE", tc.mode)
			args := append([]string{"install.sh", "--bin-dir", destination}, tc.args...)
			output, err := exec.Command("sh", args...).CombinedOutput()
			if (err != nil) != tc.failure {
				t.Fatalf("error=%v output=%s", err, output)
			}
			installed, err := os.ReadFile(original)
			if err != nil {
				t.Fatal(err)
			}
			want := string(payload)
			if tc.failure {
				want = "old binary"
			}
			if string(installed) != want {
				t.Fatalf("unexpected installed contents: %q", installed)
			}
			if !tc.failure {
				info, _ := os.Stat(original)
				if info.Mode().Perm() != 0755 {
					t.Fatal("binary not executable")
				}
				requests, _ := os.ReadFile(filepath.Join(root, "requests"))
				if !strings.Contains(string(requests), "/v0.1.0/"+asset) {
					t.Fatal("wrong asset requested", string(requests))
				}
				if len(tc.args) > 0 && strings.Contains(string(requests), "/releases/latest") {
					t.Fatal("pinned install queried latest")
				}
			}
			leftovers, _ := os.ReadDir(temporary)
			if len(leftovers) != 0 {
				t.Fatal("temporary downloads not cleaned up")
			}
			staged, _ := filepath.Glob(filepath.Join(destination, ".husage.*"))
			if len(staged) != 0 {
				t.Fatal("staged binary not cleaned up")
			}
		})
	}
}
