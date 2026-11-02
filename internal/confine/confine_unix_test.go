//go:build unix

package confine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRefusesFIFOWithoutBlocking(t *testing.T) {
	r, rootDir, _ := tree(t)
	if err := syscall.Mkfifo(filepath.Join(rootDir, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := r.ReadFile("pipe", 1<<20); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("want ErrNotRegular, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("opening a FIFO blocked")
	}
}

// A component replaced by an in-root relative symlink just before its open is refused.
func TestComponentSwapBetweenCheckAndOpenIsRefused(t *testing.T) {
	r, rootDir, _ := tree(t)
	testHookBeforeComponentOpen = func(name string) {
		if name != "a" {
			return
		}
		testHookBeforeComponentOpen = nil
		renameForSwap(t, filepath.Join(rootDir, "a"), filepath.Join(rootDir, "a-moved"))
		makeDirLink(t, "b", filepath.Join(rootDir, "a")) // relative, inside the root
	}
	t.Cleanup(func() { testHookBeforeComponentOpen = nil })
	b, err := r.ReadFile("a/f.txt", 1<<20)
	if !errors.Is(err, ErrSymlink) || strings.Contains(string(b), "DECOY") {
		t.Fatalf("a component swapped to a symlink was followed: %q %v", b, err)
	}
}

// The final name replaced by an in-root relative symlink just before its open is refused.
func TestFileSwapBetweenCheckAndOpenIsRefused(t *testing.T) {
	r, rootDir, _ := tree(t)
	testHookBeforeFinalOpen = func() {
		testHookBeforeFinalOpen = nil
		if err := os.Remove(filepath.Join(rootDir, "a", "f.txt")); err != nil {
			t.Fatal(err)
		}
		makeFileLink(t, "decoy.txt", filepath.Join(rootDir, "a", "f.txt"))
	}
	t.Cleanup(func() { testHookBeforeFinalOpen = nil })
	b, err := r.ReadFile("a/f.txt", 1<<20)
	if !errors.Is(err, ErrSymlink) || strings.Contains(string(b), "DECOY") {
		t.Fatalf("a file swapped to a symlink was followed: %q %v", b, err)
	}
}

// On unix a backslash is an ordinary filename byte, so only Split's check refuses it.
func TestRefusesBackslashNames(t *testing.T) {
	r, rootDir, _ := tree(t)
	write(t, filepath.Join(rootDir, `back\slash.txt`), "BACKSLASH-CONTENT")
	if b, err := r.ReadFile(`back\slash.txt`, 1<<20); !errors.Is(err, ErrEscape) || len(b) != 0 {
		t.Fatalf("want ErrEscape, got %q %v", b, err)
	}
}

func TestWriteModeIsExactWhateverTheUmask(t *testing.T) {
	old := syscall.Umask(0o077) // a restrictive umask: the open alone would give 0600
	defer syscall.Umask(old)
	r, rootDir, _ := tree(t)
	if err := r.WriteFileAtomic("a/shared.txt", []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(rootDir, "a", "shared.txt"))
	if err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%v err=%v", fi.Mode().Perm(), err)
	}
}

// Same-object substitutions: the original is renamed away and the name replaced by a
// relative symlink to the moved original. An identity check passes (it is the same
// object), so only a kernel no-follow open refuses them (spec 5.5 rule 7).
func TestFileSubstitutedBySymlinkToMovedOriginalIsRefused(t *testing.T) {
	r, rootDir, _ := tree(t)
	testHookBeforeFinalOpen = func() {
		testHookBeforeFinalOpen = nil
		renameForSwap(t, filepath.Join(rootDir, "a", "f.txt"), filepath.Join(rootDir, "a", "f-moved.txt"))
		makeFileLink(t, "f-moved.txt", filepath.Join(rootDir, "a", "f.txt"))
	}
	t.Cleanup(func() { testHookBeforeFinalOpen = nil })
	if b, err := r.ReadFile("a/f.txt", 1<<20); !errors.Is(err, ErrSymlink) {
		t.Fatalf("a symlink to the moved original file was followed: %q %v", b, err)
	}
}

func TestDirectorySubstitutedBySymlinkToMovedOriginalIsRefused(t *testing.T) {
	r, rootDir, _ := tree(t)
	testHookBeforeComponentOpen = func(name string) {
		if name != "a" {
			return
		}
		testHookBeforeComponentOpen = nil
		renameForSwap(t, filepath.Join(rootDir, "a"), filepath.Join(rootDir, "a-moved"))
		makeDirLink(t, "a-moved", filepath.Join(rootDir, "a"))
	}
	t.Cleanup(func() { testHookBeforeComponentOpen = nil })
	if b, err := r.ReadFile("a/f.txt", 1<<20); !errors.Is(err, ErrSymlink) {
		t.Fatalf("a symlink to the moved original directory was followed: %q %v", b, err)
	}
}

func TestRootSubstitutedBySymlinkToMovedOriginalIsRefused(t *testing.T) {
	_, rootDir, _ := tree(t)
	testHookBeforeRootOpen = func() {
		testHookBeforeRootOpen = nil
		renameForSwap(t, rootDir, rootDir+"-moved")
		makeDirLink(t, filepath.Base(rootDir)+"-moved", rootDir)
	}
	t.Cleanup(func() { testHookBeforeRootOpen = nil })
	if r, err := Open(rootDir); !errors.Is(err, ErrSymlink) {
		if r != nil {
			r.Close()
		}
		t.Fatalf("a root replaced by a symlink to the moved original was opened: %v", err)
	}
}

// A file Wikory created itself has one link and still opens, appends and replaces.
func TestSingleLinkFileStillWorks(t *testing.T) {
	r, _, _ := tree(t)
	if err := r.AppendFile("one.txt", []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendFile("one.txt", []byte("B"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := r.ReadFile("one.txt", 1<<20); err != nil || string(b) != "AB" {
		t.Fatalf("%q %v", b, err)
	}
	if err := r.ReplaceIfUnchanged("one.txt", []byte("C"), 0o644, func(cur []byte) bool { return string(cur) == "AB" }); err != nil {
		t.Fatal(err)
	}
}

// A file AppendFile creates has the requested mode, not whatever the umask leaves (G4 item 8).
func TestAppendCreatesTheRequestedModeUnderARestrictiveUmask(t *testing.T) {
	r, rootDir, _ := tree(t)
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	if err := r.AppendFile("a/new.log", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(rootDir, "a", "new.log"))
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode=%v err=%v", fi.Mode().Perm(), err)
	}
	// An existing file keeps its mode: AppendFile never re-modes a file a human owns.
	if err := os.Chmod(filepath.Join(rootDir, "a", "new.log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendFile("a/new.log", []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(filepath.Join(rootDir, "a", "new.log"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("an existing file's mode was changed to %v", fi.Mode().Perm())
	}
}
