//go:build windows

package confine

import (
	"os"
	"os/exec"
	"testing"
)

func makeDirLink(t *testing.T, target, link string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
}

func makeFileLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("file symlinks need developer mode on Windows: %v", err)
	}
}

// renameForSwap: Windows refuses to rename a directory while a handle on it is open, which
// is itself a guarantee for this test; report that the swap could not happen.
func renameForSwap(t *testing.T, from, to string) bool {
	t.Helper()
	return os.Rename(from, to) == nil
}
