//go:build unix

package confine

import (
	"os"
	"testing"
)

func makeDirLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func makeFileLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// renameForSwap renames during a swap test; unix always allows it.
func renameForSwap(t *testing.T, from, to string) bool {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
	return true
}
