//go:build spike && unix

package spikes

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func spikeTree(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "root")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{root, outside, filepath.Join(root, "real")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("INSIDE"), 0o644))
	must(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("OUTSIDE"), 0o644))
	must(t, os.Symlink("a.txt", filepath.Join(root, "inlink")))
	must(t, os.Symlink(filepath.Join(root, "a.txt"), filepath.Join(root, "abslink")))
	must(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "esc")))
	must(t, os.Symlink(outside, filepath.Join(root, "escdir")))
	return root, outside
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestOSRootRefusesEscape(t *testing.T) {
	root, _ := spikeTree(t)
	r, err := os.OpenRoot(root)
	must(t, err)
	defer r.Close()
	if _, err := r.Open("esc"); err == nil {
		t.Fatal("REFUTED V02(a): os.Root followed a symlink out of the root")
	}
	if _, err := r.Open("escdir/secret.txt"); err == nil {
		t.Fatal("REFUTED V02(a): os.Root followed a directory symlink out of the root")
	}
	t.Log("VERIFIED V02(a): os.Root refuses escaping symlinks")
}

func TestOSRootFollowsInRootSymlink(t *testing.T) {
	root, _ := spikeTree(t)
	r, err := os.OpenRoot(root)
	must(t, err)
	defer r.Close()
	f, err := r.Open("inlink")
	if err != nil {
		t.Logf("V02(b) REFUTED: os.Root refuses in-root symlinks too: %v", err)
		return
	}
	f.Close()
	t.Log("VERIFIED V02(b): os.Root follows a relative in-root symlink, so it alone does not satisfy rule 7")
	// An absolute symlink whose target is inside the root is still refused by os.Root.
	if f, err := r.Open("abslink"); err == nil {
		f.Close()
		t.Log("V02(b) note: os.Root also follows an absolute in-root symlink")
	} else {
		t.Logf("V02(b) note: os.Root refuses an absolute symlink even when its target is inside the root: %v", err)
	}
}

func TestOpenatNoFollowRefusesSymlinkComponent(t *testing.T) {
	root, _ := spikeTree(t)
	dfd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	must(t, err)
	defer unix.Close(dfd)
	_, err = unix.Openat(dfd, "escdir", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == nil {
		t.Fatal("REFUTED V02(c): openat O_NOFOLLOW opened a symlinked directory")
	}
	t.Logf("VERIFIED V02(c): openat refused the symlinked component with errno %v (ELOOP=%v ENOTDIR=%v)",
		err, errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR))
	_, err = unix.Openat(dfd, "inlink", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == nil {
		t.Fatal("REFUTED V02(c): openat O_NOFOLLOW opened a symlinked file")
	}
}

func TestRootHasRenameAndLink(t *testing.T) {
	root, _ := spikeTree(t)
	r, err := os.OpenRoot(root)
	must(t, err)
	defer r.Close()
	must(t, r.Link("a.txt", "hard.txt"))
	must(t, r.Rename("hard.txt", "moved.txt"))
	t.Log("VERIFIED V02(d): os.Root.Link and os.Root.Rename available")
}
