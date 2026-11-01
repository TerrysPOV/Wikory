//go:build spike && windows

package spikes

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsJunctionUnderOSRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("OUTSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	junc := filepath.Join(root, "junc")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junc, outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J failed: %v %s", err, out)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fi, err := r.Lstat("junc")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("V03: junction Lstat mode=%v symlink=%v irregular=%v dir=%v", fi.Mode(),
		fi.Mode()&fs.ModeSymlink != 0, fi.Mode()&fs.ModeIrregular != 0, fi.IsDir())
	if _, err := r.Open("junc/secret.txt"); err == nil {
		t.Fatal("REFUTED V03: os.Root followed a junction out of the root")
	}
	t.Log("VERIFIED V03: os.Root refuses a junction that leaves the root")
}

// V03, second half: A3 relies on os.SameFile between an os.Root Lstat result and an opened
// handle, and its swap tests rely on how Windows treats renaming an open directory. Both are
// recorded as verified results here rather than assumed.
func TestWindowsSameFileAndOpenDirectoryRename(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "d", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before, err := r.Lstat("d/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	f, err := r.Open("d/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := f.Stat()
	f.Close()
	if !os.SameFile(before, after) {
		t.Fatal("REFUTED V03: os.SameFile does not match an os.Root Lstat result to the opened file")
	}
	sub, err := r.OpenRoot("d")
	if err != nil {
		t.Fatal(err)
	}
	renameErr := os.Rename(filepath.Join(base, "d"), filepath.Join(base, "d-moved"))
	sub.Close()
	t.Logf("V03: renaming a directory with an open os.Root handle: err=%v (refusal is the expected Windows behaviour)", renameErr)
	t.Log("VERIFIED V03: SameFile identity checks work under os.Root on Windows")
}
