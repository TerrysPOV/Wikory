//go:build unix

package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// dirtyWithin runs Dirty with a deadline and fails the test if it did not return on its own
// well before it: git blocking on a FIFO would only end when the deadline killed it.
func dirtyWithin(t *testing.T, r *Repo, limit time.Duration) ([]string, error, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	start := time.Now()
	paths, err := r.Dirty(ctx, ".")
	return paths, err, time.Since(start)
}

// A FIFO at .git/info/exclude makes `git status` block on open, for ever. No git option
// skips that file, so Dirty looks at it first and refuses a repository whose exclude file is
// not a regular file, instead of leaving the caller to rely on its own deadline.
func TestFIFOAtInfoExcludeIsRefusedNotWaitedOn(t *testing.T) {
	dir := newRepo(t)
	r, err := Discover(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(r.CommonDir, "info", "exclude")
	if err := os.Remove(exclude); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	mkfifo(t, exclude)
	_, err, took := dirtyWithin(t, r, 10*time.Second)
	if !errors.Is(err, ErrUnsafeExclude) || took > 5*time.Second {
		t.Fatalf("Dirty with a FIFO at info/exclude: err=%v after %v, want ErrUnsafeExclude promptly", err, took)
	}
}

// A symlink to a FIFO is just as blocking, because git follows it.
func TestSymlinkToFIFOAtInfoExcludeIsRefused(t *testing.T) {
	dir := newRepo(t)
	r, _ := Discover(context.Background(), dir)
	fifo := filepath.Join(t.TempDir(), "fifo")
	mkfifo(t, fifo)
	exclude := filepath.Join(r.CommonDir, "info", "exclude")
	_ = os.Remove(exclude)
	if err := os.Symlink(fifo, exclude); err != nil {
		t.Fatal(err)
	}
	_, err, took := dirtyWithin(t, r, 10*time.Second)
	if !errors.Is(err, ErrUnsafeExclude) || took > 5*time.Second {
		t.Fatalf("err=%v after %v, want ErrUnsafeExclude promptly", err, took)
	}
}

// The check must not refuse an ordinary exclude file, and the patterns in it still apply.
func TestRegularInfoExcludeStillWorks(t *testing.T) {
	dir := newRepo(t)
	r, _ := Discover(context.Background(), dir)
	if err := os.WriteFile(filepath.Join(r.CommonDir, "info", "exclude"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"x.log", "y.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("z"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, err, _ := dirtyWithin(t, r, 10*time.Second)
	if err != nil || len(paths) != 1 || paths[0] != "y.txt" {
		t.Fatalf("paths=%v err=%v, want only y.txt", paths, err)
	}
}

// core.excludesFile names a file git reads on every status. Pointed at a FIFO (in the user's
// global config or the repository's own) it blocks git, so the status call overrides it with an
// empty value: this is the same hang as above from a different file.
func TestExcludesFileAtAFIFOInConfigDoesNotBlockStatus(t *testing.T) {
	dir := newRepo(t)
	fifo := filepath.Join(t.TempDir(), "ignore-fifo")
	mkfifo(t, fifo)
	gitT(t, dir, "config", "core.excludesFile", fifo)
	r, err := Discover(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err, took := dirtyWithin(t, r, 10*time.Second)
	if err != nil || took > 5*time.Second {
		t.Fatalf("Dirty with core.excludesFile at a FIFO: err=%v after %v", err, took)
	}
}

// A link to an ordinary file is fine (git follows it), so the check looks through links rather
// than refusing every one.
func TestSymlinkToARegularInfoExcludeStillWorks(t *testing.T) {
	dir := newRepo(t)
	r, _ := Discover(context.Background(), dir)
	target := filepath.Join(t.TempDir(), "exclude-target")
	if err := os.WriteFile(target, []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(r.CommonDir, "info", "exclude")
	_ = os.Remove(exclude)
	if err := os.Symlink(target, exclude); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"x.log", "y.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("z"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, err, _ := dirtyWithin(t, r, 10*time.Second)
	if err != nil || len(paths) != 1 || paths[0] != "y.txt" {
		t.Fatalf("paths=%v err=%v, want only y.txt", paths, err)
	}
}
