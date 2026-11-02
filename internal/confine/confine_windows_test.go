//go:build windows

package confine

import (
	"errors"
	"os"
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

// hardlinkOutside links a file outside the root to a name inside it with os.Link, as an archive
// that preserves hard links could. It is a regular file with the same identity, so a symlink
// check and a regular-file check both pass it: only the link count refuses it. Mirrors the unix
// helper of the same name.
func hardlinkOutside(t *testing.T) (r *Root, rootDir, outsideFile string) {
	t.Helper()
	r, rootDir, outside := tree(t)
	outsideFile = filepath.Join(outside, "secret.txt")
	if err := os.Link(outsideFile, filepath.Join(rootDir, "hl.txt")); err != nil {
		t.Skipf("hard links are not available here: %v", err)
	}
	return r, rootDir, outsideFile
}

func outsideIntact(t *testing.T, outsideFile string) {
	t.Helper()
	if b, err := os.ReadFile(outsideFile); err != nil || string(b) != "OUTSIDE-SECRET" {
		t.Fatalf("the outside file was modified: %q %v", b, err)
	}
}

func TestReadRefusesHardlinkToOutsideFile(t *testing.T) {
	r, _, outsideFile := hardlinkOutside(t)
	b, err := r.ReadFile("hl.txt", 1<<20)
	if !errors.Is(err, ErrHardlink) || len(b) != 0 {
		t.Fatalf("want ErrHardlink and no bytes, got %q %v", b, err)
	}
	outsideIntact(t, outsideFile)
}

func TestAppendRefusesHardlinkToOutsideFile(t *testing.T) {
	r, _, outsideFile := hardlinkOutside(t)
	if err := r.AppendFile("hl.txt", []byte("APPENDED"), 0o644); !errors.Is(err, ErrHardlink) {
		t.Fatalf("append through a hard link: want ErrHardlink, got %v", err)
	}
	outsideIntact(t, outsideFile)
}

// want accepts anything, so only the link-count check can refuse this replace.
func TestReplaceRefusesHardlinkToOutsideFile(t *testing.T) {
	r, rootDir, outsideFile := hardlinkOutside(t)
	err := r.ReplaceIfUnchanged("hl.txt", []byte("NEW"), 0o644, func([]byte) bool { return true })
	if !errors.Is(err, ErrHardlink) {
		t.Fatalf("replace of a hard-linked file: want ErrHardlink, got %v", err)
	}
	outsideIntact(t, outsideFile)
	b, _ := os.ReadFile(filepath.Join(rootDir, "hl.txt"))
	if string(b) != "OUTSIDE-SECRET" || len(leftovers(t, rootDir)) != 0 {
		t.Fatalf("the refused target must be put back untouched: content=%q leftovers=%v", b, leftovers(t, rootDir))
	}
}
