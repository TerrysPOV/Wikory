//go:build windows

package confine

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestRefusesJunctionInsideRoot(t *testing.T) {
	r, rootDir, _ := tree(t)
	makeDirLink(t, filepath.Join(rootDir, "a"), filepath.Join(rootDir, "j"))
	if _, err := r.ReadFile("j/f.txt", 1<<20); !errors.Is(err, ErrSymlink) {
		t.Fatalf("an in-root junction must be refused, got %v", err)
	}
}

func TestRefusesWindowsSpecialNames(t *testing.T) {
	for _, rel := range []string{"CON", "a/nul.txt", "a/f.txt:stream", "a/trailing.", "a/trailing "} {
		if _, err := Split(rel); !errors.Is(err, ErrEscape) {
			t.Errorf("%q: want ErrEscape, got %v", rel, err)
		}
	}
}
