//go:build unix

package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// touchAfterCommit rewrites path with same-size, different content and a modification time
// two hours ahead, so git cannot tell from stat data alone that it is unchanged and must
// run the file through its clean filter to compare. Without this a filter might never run
// and a "the marker is absent" assertion would pass for the wrong reason.
func touchAfterCommit(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

// A subsection name may contain '=': passed as `-c key=` git splits at the first '=', the
// real key is not overridden and the repository's command runs. Overrides must travel with
// the key and value separate.
func TestHostileConfigKeyNamesAreNeutralised(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	names := []string{"a=b", "x=y=z", "trail="}
	var attrs strings.Builder
	for i, n := range names {
		f := fmt.Sprintf("f%d.txt", i)
		fmt.Fprintf(&attrs, "%s filter=%s diff=%s\n", f, n, n)
		if err := os.WriteFile(filepath.Join(dir, f), []byte("alpha\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte(attrs.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "files")
	var markers []string
	for i, n := range names {
		fm := filepath.Join(dir, fmt.Sprintf("PWNED-FILTER-%d", i))
		tm := filepath.Join(dir, fmt.Sprintf("PWNED-TEXTCONV-%d", i))
		markers = append(markers, fm, tm)
		gitT(t, dir, "config", "filter."+n+".clean", "sh -c 'touch "+fm+"; cat'")
		gitT(t, dir, "config", "diff."+n+".textconv", "touch "+tm+"; cat")
		touchAfterCommit(t, filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), "alphb\n")
	}
	r, err := Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Dirty(ctx, "."); err != nil {
		t.Fatal(err)
	}
	// git refuses to run a textconv command that has been emptied, so this diff may fail;
	// what matters is that no marker appears.
	_, _ = r.git(ctx, nil, "diff", "--no-ext-diff", "--textconv", "HEAD", "--")
	for _, m := range markers {
		if _, err := os.Stat(m); err == nil {
			t.Errorf("a driver named with '=' ran: %s exists", filepath.Base(m))
		}
	}
}

// A submodule has its own config and attributes, which the superproject's neutralisation
// list has never seen. status must not descend into it, even when the repository says the
// submodule must always be checked (submodule.<name>.ignore=none).
func TestSubmoduleConfigIsNeverRun(t *testing.T) {
	ctx := context.Background()
	src := newRepo(t)
	if err := os.WriteFile(filepath.Join(src, ".gitattributes"), []byte("*.txt filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, src, "add", ".gitattributes")
	gitT(t, src, "commit", "-qm", "attributes")
	dir := newRepo(t)
	gitT(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", src, "sub")
	gitT(t, dir, "commit", "-qm", "add submodule")
	gitT(t, dir, "config", "submodule.sub.ignore", "none")
	marker := filepath.Join(dir, "PWNED-SUBMODULE")
	gitT(t, filepath.Join(dir, "sub"), "config", "filter.evil.clean", "sh -c 'touch "+marker+"; cat'")
	touchAfterCommit(t, filepath.Join(dir, "sub", "a.txt"), "alphb\n")
	r, err := Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Dirty(ctx, "."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a filter defined in a submodule's own config ran")
	}
}
