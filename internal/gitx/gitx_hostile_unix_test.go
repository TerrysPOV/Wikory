//go:build unix

package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A repository's own config can name a clean filter that runs a command; git status and
// hash-object would run it for a modified file. Wikory must never run it.
func TestRepositoryCleanFilterIsNeverRun(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	marker := filepath.Join(dir, "PWNED-FILTER")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "config", "filter.evil.clean", "sh -c 'touch "+marker+"; cat'")
	gitT(t, dir, "config", "filter.evil.required", "true")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Dirty(ctx, "."); err != nil {
		t.Fatal(err)
	}
	if _, err := r.HashBlob(ctx, "a.txt", []byte("changed\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's clean filter command ran")
	}
}
