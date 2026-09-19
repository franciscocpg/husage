package profile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareDoesNotRegisterAndRegisterPreservesProfiles(t *testing.T) {
	s := Store{Home: t.TempDir()}
	dir, err := s.Prepare(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal(info, err)
	}
	if _, err := os.Stat(s.ConfigPath()); !os.IsNotExist(err) {
		t.Fatal("prepare registered before login")
	}
	second, err := s.Prepare(context.Background(), "personal")
	if err != nil {
		t.Fatal(err)
	}
	// Another login may complete while the first one's browser flow is open.
	if _, err = s.Register(context.Background(), "personal"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Register(context.Background(), "work"); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(s.ConfigPath())
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	b, err := os.ReadFile(s.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	if json.Unmarshal(b, &paths) != nil || len(paths) != 3 || paths[0] != "current" || paths[1] != second || paths[2] != dir {
		t.Fatal(string(b))
	}
	if _, err = s.Prepare(context.Background(), "work"); err == nil {
		t.Fatal("duplicate was accepted")
	}
	if _, err = s.Register(context.Background(), "work"); err == nil {
		t.Fatal("duplicate was registered")
	}
	after, _ := os.ReadFile(s.ConfigPath())
	if string(after) != string(b) {
		t.Fatal("duplicate changed profile list")
	}
	if _, err = os.Stat(s.ConfigPath() + ".lock"); !os.IsNotExist(err) {
		t.Fatal("lock was not released")
	}
}

func TestInvalidNameAndCancellationHaveNoSideEffects(t *testing.T) {
	for _, name := range []string{"", "../escape", "a/b", "/tmp/escape", "with space", ".", "-start", strings.Repeat("a", 49)} {
		s := Store{Home: t.TempDir()}
		if _, err := s.Prepare(context.Background(), name); err == nil {
			t.Fatalf("accepted %q", name)
		}
		entries, _ := os.ReadDir(s.Home)
		if len(entries) != 0 {
			t.Fatal("validation created files")
		}
	}
	s := Store{Home: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Prepare(ctx, "work"); err == nil {
		t.Fatal("ignored cancellation")
	}
	entries, _ := os.ReadDir(s.Home)
	if len(entries) != 0 {
		t.Fatal("cancelled preparation created files")
	}
	if _, err := s.Register(context.Background(), "missing"); err == nil {
		t.Fatal("registered without directory")
	}
}

func TestInvalidExistingListAndDirectoryArePreserved(t *testing.T) {
	for _, data := range []string{`{`, `[]`, `null`, `[""]`, `["relative"]`} {
		s := Store{Home: t.TempDir()}
		os.MkdirAll(filepath.Dir(s.ConfigPath()), 0700)
		os.WriteFile(s.ConfigPath(), []byte(data), 0600)
		if _, err := s.Prepare(context.Background(), "work"); err == nil {
			t.Fatal("accepted invalid list")
		}
		b, _ := os.ReadFile(s.ConfigPath())
		if string(b) != data {
			t.Fatal("existing list overwritten")
		}
		if _, err := os.Stat(s.Directory("work")); !os.IsNotExist(err) {
			t.Fatal("created directory on failed preparation")
		}
	}
	s := Store{Home: t.TempDir()}
	os.MkdirAll(s.Directory("work"), 0700)
	credentials := filepath.Join(s.Directory("work"), ".credentials.json")
	os.WriteFile(credentials, []byte("existing data"), 0600)
	if _, err := s.Prepare(context.Background(), "work"); err == nil {
		t.Fatal("reused existing directory")
	}
	b, _ := os.ReadFile(credentials)
	if string(b) != "existing data" {
		t.Fatal("existing file modified")
	}
}

func TestSaveFailureKeepsLoginFilesAndAllowsRetry(t *testing.T) {
	s := Store{Home: t.TempDir()}
	dir, err := s.Prepare(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	original := `["current","~/existing"]`
	os.WriteFile(s.ConfigPath(), []byte(original), 0600)
	credentials := filepath.Join(dir, ".credentials.json")
	os.WriteFile(credentials, []byte("fake login result"), 0600)
	os.WriteFile(s.ConfigPath()+".lock", []byte("other writer"), 0600)
	if _, err = s.Register(context.Background(), "work"); err == nil {
		t.Fatal("ignored another writer")
	}
	b, _ := os.ReadFile(s.ConfigPath())
	if string(b) != original {
		t.Fatal("existing list changed")
	}
	b, _ = os.ReadFile(credentials)
	if string(b) != "fake login result" {
		t.Fatal("login files deleted")
	}
	b, _ = os.ReadFile(s.ConfigPath() + ".lock")
	if string(b) != "other writer" {
		t.Fatal("removed another writer's lock")
	}
	os.Remove(s.ConfigPath() + ".lock")
	if _, err = s.Register(context.Background(), "work"); err != nil {
		t.Fatal("save retry failed", err)
	}
}

func TestSymlinkedConfigurationIsNotReplaced(t *testing.T) {
	s := Store{Home: t.TempDir()}
	os.MkdirAll(filepath.Dir(s.ConfigPath()), 0700)
	target := filepath.Join(s.Home, "original.json")
	os.WriteFile(target, []byte(`["current"]`), 0600)
	if err := os.Symlink(target, s.ConfigPath()); err != nil {
		t.Skip(err)
	}
	if _, err := s.Prepare(context.Background(), "work"); err == nil {
		t.Fatal("accepted symlink")
	}
	info, _ := os.Lstat(s.ConfigPath())
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced existing link")
	}
}
