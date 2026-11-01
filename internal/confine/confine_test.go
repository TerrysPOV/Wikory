package confine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tree: base/root (the authorised root) and base/outside (must never be readable through root).
func tree(t *testing.T) (root *Root, rootDir, outside string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	rootDir = filepath.Join(base, "root")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(rootDir, "a"), filepath.Join(rootDir, "b"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(rootDir, "a", "f.txt"), "INSIDE")
	write(t, filepath.Join(rootDir, "b", "f.txt"), "DECOY")
	write(t, filepath.Join(rootDir, "a", "decoy.txt"), "DECOY")
	write(t, filepath.Join(outside, "secret.txt"), "OUTSIDE-SECRET")
	write(t, filepath.Join(base, "sibling.txt"), "SIBLING-SECRET")
	r, err := Open(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, rootDir, outside
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadInsideRoot(t *testing.T) {
	r, _, _ := tree(t)
	b, err := r.ReadFile("a/f.txt", 1<<20)
	if err != nil || string(b) != "INSIDE" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestRefusesEscapingNames(t *testing.T) {
	r, _, _ := tree(t)
	// "../sibling.txt" exists and is readable, so only a name check can stop it.
	for _, rel := range []string{"../sibling.txt", "a/../../sibling.txt", "/etc/hosts", `a\f.txt`, "C:/x", "", "a//f.txt", "./a/f.txt", "a/./f.txt", "a/f.txt\x00"} {
		b, err := r.ReadFile(rel, 1<<20)
		if !errors.Is(err, ErrEscape) || strings.Contains(string(b), "SECRET") {
			t.Errorf("rel=%q: want ErrEscape, got %q %v", rel, b, err)
		}
	}
}

func TestRefusesSymlinkedFile(t *testing.T) {
	r, rootDir, outside := tree(t)
	makeFileLink(t, filepath.Join(outside, "secret.txt"), filepath.Join(rootDir, "link.txt"))
	b, err := r.ReadFile("link.txt", 1<<20)
	if !errors.Is(err, ErrSymlink) || len(b) != 0 {
		t.Fatalf("want ErrSymlink and no bytes, got %q %v", b, err)
	}
}

func TestRefusesInRootSymlink(t *testing.T) {
	r, rootDir, _ := tree(t)
	makeFileLink(t, filepath.Join(rootDir, "a", "f.txt"), filepath.Join(rootDir, "inlink.txt"))
	if _, err := r.ReadFile("inlink.txt", 1<<20); !errors.Is(err, ErrSymlink) {
		t.Fatalf("rule 7 refuses every symlink, got %v", err)
	}
}

func TestRefusesSymlinkedDirectory(t *testing.T) {
	r, rootDir, outside := tree(t)
	makeDirLink(t, outside, filepath.Join(rootDir, "d"))
	b, err := r.ReadFile("d/secret.txt", 1<<20)
	if !errors.Is(err, ErrSymlink) || len(b) != 0 {
		t.Fatalf("want ErrSymlink, got %q %v", b, err)
	}
	if err := r.WriteFileAtomic("d/new.txt", []byte("x"), 0o644); !errors.Is(err, ErrSymlink) {
		t.Fatalf("write through a symlinked directory: want ErrSymlink, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a file was created outside the root")
	}
}

func TestDirectorySwapAfterWalkCannotRedirect(t *testing.T) {
	r, rootDir, outside := tree(t)
	write(t, filepath.Join(outside, "f.txt"), "OUTSIDE-SECRET")
	swapped := false
	testHookBeforeFinalOpen = func() {
		testHookBeforeFinalOpen = nil
		if swapped = renameForSwap(t, filepath.Join(rootDir, "a"), filepath.Join(rootDir, "a-moved")); swapped {
			makeDirLink(t, outside, filepath.Join(rootDir, "a"))
		}
	}
	t.Cleanup(func() { testHookBeforeFinalOpen = nil })
	b, err := r.ReadFile("a/f.txt", 1<<20)
	if strings.Contains(string(b), "OUTSIDE") {
		t.Fatalf("the swap redirected the open outside the root: %q", b)
	}
	if err != nil || string(b) != "INSIDE" {
		t.Fatalf("want the original directory's file through the held handle (swapped=%v), got %q %v", swapped, b, err)
	}
}

func TestOpenRefusesSymlinkedRoot(t *testing.T) {
	_, rootDir, _ := tree(t)
	link := rootDir + "-link"
	makeDirLink(t, rootDir, link)
	if _, err := Open(link); !errors.Is(err, ErrSymlink) {
		t.Fatalf("a symlinked final root component must be refused, got %v", err)
	}
}

func TestRootSwapDuringOpenIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to rename a directory that is open, so this swap cannot happen there")
	}
	_, rootDir, _ := tree(t)
	other := rootDir + "-other"
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	testHookBeforeRootOpen = func() {
		testHookBeforeRootOpen = nil
		if err := os.Rename(rootDir, rootDir+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(other, rootDir); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { testHookBeforeRootOpen = nil })
	if _, err := Open(rootDir); !errors.Is(err, ErrSymlink) {
		t.Fatalf("a root replaced between check and open must be refused, got %v", err)
	}
}

func TestRootHandleSurvivesReplacementAfterOpen(t *testing.T) {
	r, rootDir, _ := tree(t)
	if !renameForSwap(t, rootDir, rootDir+"-moved") {
		t.Skip("this platform refuses to rename an open directory, which is the guarantee")
	}
	if err := os.MkdirAll(filepath.Join(rootDir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(rootDir, "a", "f.txt"), "REPLACEMENT")
	if b, err := r.ReadFile("a/f.txt", 1<<20); err != nil || string(b) != "INSIDE" {
		t.Fatalf("access must stay with the directory that was opened: %q %v", b, err)
	}
}

func TestWriteAtomicRefusesSymlinkTarget(t *testing.T) {
	r, rootDir, outside := tree(t)
	makeFileLink(t, filepath.Join(outside, "secret.txt"), filepath.Join(rootDir, "t.txt"))
	if err := r.WriteFileAtomic("t.txt", []byte("CLOBBER"), 0o644); !errors.Is(err, ErrSymlink) {
		t.Fatalf("want ErrSymlink, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret.txt")); string(b) != "OUTSIDE-SECRET" {
		t.Fatalf("outside file changed: %q", b)
	}
	if fi, err := os.Lstat(filepath.Join(rootDir, "t.txt")); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatal("the symlink itself must be left in place, not replaced")
	}
}

// entries lists a directory's names.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func leftovers(t *testing.T, dir string) []string {
	var out []string
	for _, n := range entries(t, dir) {
		if strings.HasPrefix(n, BackupPrefix) || strings.HasPrefix(n, TempPrefix) {
			out = append(out, n)
		}
	}
	return out
}

func unchanged(cur []byte) bool { return string(cur) == "INSIDE" }

func TestReplaceInstallsWhenUnchanged(t *testing.T) {
	r, rootDir, _ := tree(t)
	if err := r.ReplaceIfUnchanged("a/f.txt", []byte("NEW"), 0o644, unchanged); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(rootDir, "a", "f.txt"))
	if string(b) != "NEW" || len(leftovers(t, filepath.Join(rootDir, "a"))) != 0 {
		t.Fatalf("content=%q leftovers=%v", b, leftovers(t, filepath.Join(rootDir, "a")))
	}
}

// A human edit before the move-aside is judged and refused; the edit stays, nothing is left.
func TestReplaceRefusesEditBeforeMoveAside(t *testing.T) {
	r, rootDir, _ := tree(t)
	testHookBeforeMoveAside = func() { write(t, filepath.Join(rootDir, "a", "f.txt"), "HUMAN EDIT") }
	t.Cleanup(func() { testHookBeforeMoveAside = nil })
	err := r.ReplaceIfUnchanged("a/f.txt", []byte("NEW"), 0o644, unchanged)
	b, _ := os.ReadFile(filepath.Join(rootDir, "a", "f.txt"))
	if !errors.Is(err, ErrChanged) || string(b) != "HUMAN EDIT" || len(leftovers(t, filepath.Join(rootDir, "a"))) != 0 {
		t.Fatalf("err=%v content=%q leftovers=%v", err, b, leftovers(t, filepath.Join(rootDir, "a")))
	}
}

// humanEditAt writes the target by path, as an editor saving the file would, at a hook.
func humanEditAt(t *testing.T, hook *func(), rootDir string) {
	*hook = func() { write(t, filepath.Join(rootDir, "a", "f.txt"), "HUMAN EDIT") }
	t.Cleanup(func() { *hook = nil })
}

// keptBoth: the human file is at the target and the original survives as a backup.
func keptBoth(t *testing.T, err error, rootDir string) {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(rootDir, "a", "f.txt"))
	var backup string
	for _, n := range entries(t, filepath.Join(rootDir, "a")) {
		if strings.HasPrefix(n, BackupPrefix) {
			c, _ := os.ReadFile(filepath.Join(rootDir, "a", n))
			backup = string(c)
		}
		if strings.HasPrefix(n, TempPrefix) {
			t.Fatalf("temp file left behind: %s", n)
		}
	}
	if !errors.Is(err, ErrChanged) || string(b) != "HUMAN EDIT" || backup != "INSIDE" {
		t.Fatalf("human edit not kept: err=%v content=%q backup=%q", err, b, backup)
	}
}

func TestReplaceKeepsEditMadeAfterMoveAside(t *testing.T) {
	r, rootDir, _ := tree(t)
	humanEditAt(t, &testHookAfterMoveAside, rootDir)
	keptBoth(t, r.ReplaceIfUnchanged("a/f.txt", []byte("NEW"), 0o644, unchanged), rootDir)
}

func TestReplaceKeepsEditMadeAfterCompare(t *testing.T) {
	r, rootDir, _ := tree(t)
	humanEditAt(t, &testHookAfterCompare, rootDir)
	keptBoth(t, r.ReplaceIfUnchanged("a/f.txt", []byte("NEW"), 0o644, unchanged), rootDir)
}

func TestReplaceOfMissingTargetIsChanged(t *testing.T) {
	r, rootDir, _ := tree(t)
	err := r.ReplaceIfUnchanged("a/none.txt", []byte("NEW"), 0o644, unchanged)
	if _, serr := os.Stat(filepath.Join(rootDir, "a", "none.txt")); !errors.Is(err, ErrChanged) || serr == nil {
		t.Fatalf("err=%v stat=%v", err, serr)
	}
}

// A crash after the move-aside leaves the target missing; recovery puts the backup back.
func TestRestoreBackupsPutsBackAMissingTarget(t *testing.T) {
	r, rootDir, _ := tree(t)
	bak := backupName("f.txt")
	if err := os.Rename(filepath.Join(rootDir, "a", "f.txt"), filepath.Join(rootDir, "a", bak)); err != nil {
		t.Fatal(err)
	}
	kept, err := r.RestoreBackups("a", func(string, []byte) bool { return false })
	b, _ := os.ReadFile(filepath.Join(rootDir, "a", "f.txt"))
	if err != nil || len(kept) != 0 || string(b) != "INSIDE" || len(leftovers(t, filepath.Join(rootDir, "a"))) != 0 {
		t.Fatalf("err=%v kept=%v content=%q", err, kept, b)
	}
}

func TestWriteAtomicReplacesAndLeavesNoTemp(t *testing.T) {
	r, rootDir, _ := tree(t)
	if err := r.WriteFileAtomic("a/f.txt", []byte("NEW"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(rootDir, "a", "f.txt"))
	ents, _ := os.ReadDir(filepath.Join(rootDir, "a"))
	if string(b) != "NEW" || len(ents) != 2 {
		t.Fatalf("content=%q entries=%d", b, len(ents))
	}
}

func TestCreateExclusiveNeverOverwrites(t *testing.T) {
	r, rootDir, _ := tree(t)
	if err := r.CreateExclusive("a/once.txt", []byte("FIRST"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.CreateExclusive("a/once.txt", []byte("SECOND"), 0o600); !errors.Is(err, ErrExist) {
		t.Fatalf("want ErrExist, got %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(rootDir, "a", "once.txt")); string(b) != "FIRST" {
		t.Fatalf("content=%q", b)
	}
}

func TestReadFileSizeLimit(t *testing.T) {
	r, _, _ := tree(t)
	if _, err := r.ReadFile("a/f.txt", 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a 6-byte file over a 5-byte limit: want ErrTooLarge, got %v", err)
	}
}

func TestMkdirAllAppendReadDirRemove(t *testing.T) {
	r, rootDir, _ := tree(t)
	if err := r.MkdirAll("x/y", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendFile("x/y/log.md", []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.AppendFile("x/y/log.md", []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(rootDir, "x", "y", "log.md"))
	ents, err := r.ReadDir("x/y")
	if string(b) != "one\ntwo\n" || err != nil || len(ents) != 1 || ents[0].Name() != "log.md" {
		t.Fatalf("append=%q readdir=%v %v", b, ents, err)
	}
	if err := r.Remove("x/y/log.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rootDir, "x", "y", "log.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("remove did not remove")
	}
}

// A directory at the target cannot be hard-linked back, so it must be refused before it is
// ever moved aside (G4 item 6).
func TestReplaceRefusesADirectoryTarget(t *testing.T) {
	r, rootDir, _ := tree(t)
	dir := filepath.Join(rootDir, "a", "sub")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "keep.txt"), "KEEP")
	err := r.ReplaceIfUnchanged("a/sub", []byte("NEW"), 0o644, func([]byte) bool { return true })
	b, rerr := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if !errors.Is(err, ErrNotRegular) || rerr != nil || string(b) != "KEEP" || len(leftovers(t, filepath.Join(rootDir, "a"))) != 0 {
		t.Fatalf("a directory target was not refused untouched: err=%v keep=%q %v leftovers=%v", err, b, rerr, leftovers(t, filepath.Join(rootDir, "a")))
	}
}

// When the restore link fails because a human recreated the file, the error must say where
// the backup is, or the original content is stranded with no pointer to it (G4 item 7).
func TestReplaceRestoreFailureNamesTheBackup(t *testing.T) {
	r, rootDir, _ := tree(t)
	humanEditAt(t, &testHookAfterMoveAside, rootDir)
	err := r.ReplaceIfUnchanged("a/f.txt", []byte("NEW"), 0o644, func([]byte) bool { return false })
	var backup string
	for _, n := range entries(t, filepath.Join(rootDir, "a")) {
		if strings.HasPrefix(n, BackupPrefix) {
			backup = n
		}
	}
	if !errors.Is(err, ErrChanged) || backup == "" || !strings.Contains(fmt.Sprint(err), backup) {
		t.Fatalf("the error does not name the stranded backup %q: %v", backup, err)
	}
}

func TestRenameMovesAFileWithinItsDirectory(t *testing.T) {
	r, rootDir, _ := tree(t)
	write(t, filepath.Join(rootDir, "a", "old.txt"), "OLD")
	write(t, filepath.Join(rootDir, "a", "new.txt"), "PREVIOUS")
	if err := r.Rename("a/old.txt", "new.txt"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(rootDir, "a", "new.txt"))
	_, serr := os.Stat(filepath.Join(rootDir, "a", "old.txt"))
	if string(b) != "OLD" || !errors.Is(serr, fs.ErrNotExist) {
		t.Fatalf("content=%q stat=%v", b, serr)
	}
	for _, bad := range []string{"../escape.txt", "x/y.txt", "", `..`} {
		if err := r.Rename("a/new.txt", bad); !errors.Is(err, ErrEscape) {
			t.Errorf("new name %q: want ErrEscape, got %v", bad, err)
		}
	}
}
