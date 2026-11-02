package gitx

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests run on every platform. The marker command a hostile repository config names is
// this test binary itself, run as a helper (see TestHelperTouchMarker), so no shell utility
// such as touch or cat is needed: only git, which every platform here has.

// TestHelperTouchMarker is not a test. Run as a git filter, textconv or fsmonitor command with
// `-- <marker> ...` after the test flags, it creates <marker> and copies stdin to stdout, as a
// clean filter must. In an ordinary run there is nothing after the flags and it returns.
func TestHelperTouchMarker(t *testing.T) {
	args := flag.Args()
	if len(args) == 0 {
		return
	}
	if err := os.WriteFile(args[0], []byte("ran\n"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_, _ = io.Copy(os.Stdout, os.Stdin)
	os.Exit(0)
}

// markerCommand is a shell command line that creates marker when git runs it. Every path uses
// forward slashes and single quotes, which both sh and git for Windows's sh accept.
func markerCommand(t *testing.T, marker string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return "'" + filepath.ToSlash(exe) + "' '-test.run=^TestHelperTouchMarker$' -- '" + filepath.ToSlash(marker) + "'"
}

// requireMarkerCommandWorks proves the helper creates its marker when a shell runs the command
// line, so an absent marker later means git did not run it, not that the helper cannot run.
func requireMarkerCommandWorks(t *testing.T) {
	t.Helper()
	dir := newRepo(t)
	marker := filepath.Join(t.TempDir(), "proof")
	gitT(t, dir, "config", "alias.proof", "!"+markerCommand(t, marker))
	gitT(t, dir, "proof")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the marker command does not run under git's shell: %v", err)
	}
}

// A repository's own config can name a clean filter that runs a command; git status and
// hash-object would run it for a modified file. Wikory must never run it.
func TestRepositoryCleanFilterIsNeverRun(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	marker := filepath.Join(dir, "PWNED-FILTER")
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("*.txt filter=evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requireMarkerCommandWorks(t)
	gitT(t, dir, "config", "filter.evil.clean", markerCommand(t, marker))
	gitT(t, dir, "config", "filter.evil.required", "true")
	// Same size and a later mtime, so git has to run the file through the clean filter to compare:
	// a different size would be reported as modified without ever running it, and the marker would
	// be absent for the wrong reason.
	touchAfterCommit(t, filepath.Join(dir, "a.txt"), "alphb\n")
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
	requireMarkerCommandWorks(t)
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
		gitT(t, dir, "config", "filter."+n+".clean", markerCommand(t, fm))
		gitT(t, dir, "config", "diff."+n+".textconv", markerCommand(t, tm))
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
	requireMarkerCommandWorks(t)
	gitT(t, filepath.Join(dir, "sub"), "config", "filter.evil.clean", markerCommand(t, marker))
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

func TestRepositoryFsmonitorIsNeverRun(t *testing.T) {
	dir := newRepo(t)
	marker := filepath.Join(dir, "PWNED")
	requireMarkerCommandWorks(t)
	gitT(t, dir, "config", "core.fsmonitor", markerCommand(t, marker))
	r, _ := Discover(context.Background(), dir)
	if _, err := r.Dirty(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's core.fsmonitor command ran")
	}
}
