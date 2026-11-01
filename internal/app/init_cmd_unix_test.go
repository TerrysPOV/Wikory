//go:build unix

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitRefusesSymlinkedProjectMemory(t *testing.T) {
	dir := newGitRepo(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "project-memory")); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := newCLI(t).run(dir, "init")
	ents, _ := os.ReadDir(outside)
	if code == ExitOK || len(ents) != 0 || !strings.Contains(errOut, "symlink") {
		t.Fatalf("code=%d entries=%d stderr=%s", code, len(ents), errOut)
	}
}
