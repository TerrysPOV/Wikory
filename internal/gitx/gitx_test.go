package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDiscoverMainAndWorktree(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Discover(ctx, filepath.Join(dir, "sub"))
	if err != nil || r.Top != dir || r.CommonDir != filepath.Join(dir, ".git") {
		t.Fatalf("%+v %v", r, err)
	}
	wt := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-wt")
	gitT(t, dir, "worktree", "add", "-q", wt)
	w, err := Discover(ctx, wt)
	if err != nil || w.Top != wt || w.CommonDir != r.CommonDir || w.GitDir == r.GitDir {
		t.Fatalf("worktree %+v %v", w, err)
	}
}

func TestNotARepo(t *testing.T) {
	if _, err := Discover(context.Background(), t.TempDir()); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("want ErrNotRepo, got %v", err)
	}
}

func TestHeadBlobsAndHashBlob(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	r, _ := Discover(ctx, dir)
	want := gitT(t, dir, "rev-parse", "HEAD:a.txt")
	got, err := r.HeadBlobs(ctx, []string{"a.txt", "missing.txt"})
	if err != nil || got["a.txt"] != want || len(got) != 1 {
		t.Fatalf("%v %v want %s", got, err, want)
	}
	same, err := r.HashBlob(ctx, "a.txt", []byte("alpha\n"))
	if err != nil || same != want {
		t.Fatalf("hash of committed content: %s %v", same, err)
	}
	diff, _ := r.HashBlob(ctx, "a.txt", []byte("beta\n"))
	if diff == want {
		t.Fatal("changed content must hash differently")
	}
}

func TestLiteralPathspecs(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := Discover(ctx, dir)
	// git status globs pathspecs unless --literal-pathspecs is set; a claim path such as
	// "*.md" must name a file called *.md, never every markdown file.
	got, err := r.Dirty(ctx, "*.md")
	if err != nil || len(got) != 0 {
		t.Fatalf("a glob matched as a pathspec: %v %v", got, err)
	}
}

func TestCommitsHeadAndBranch(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	r, _ := Discover(ctx, dir)
	head, err := r.HeadCommit(ctx)
	if err != nil || len(head) < 40 {
		t.Fatalf("%q %v", head, err)
	}
	if ok, _ := r.CommitExists(ctx, head[:12]); !ok {
		t.Fatal("HEAD should exist")
	}
	if ok, err := r.CommitExists(ctx, "--output=/tmp/x"); ok || err == nil {
		t.Fatal("an option-shaped sha must be refused, not passed to git")
	}
	if ok, _ := r.CommitExists(ctx, "0123456789abcdef0123456789abcdef01234567"); ok {
		t.Fatal("an absent commit must not exist")
	}
	if tree, _ := r.HeadTree(ctx); len(tree) < 40 {
		t.Fatalf("tree %q", tree)
	}
	if b := r.Branch(ctx); b != "main" {
		t.Fatalf("branch %q", b)
	}
}

func TestDirtyAndOrigin(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	gitT(t, dir, "remote", "add", "origin", "git@github.com:o/r.git")
	r, _ := Discover(ctx, dir)
	if r.OriginURL(ctx) != "git@github.com:o/r.git" {
		t.Fatal("origin url")
	}
	_ = os.MkdirAll(filepath.Join(dir, "project-memory", "wiki"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "project-memory", "wiki", "p.md"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644)
	got, err := r.Dirty(ctx, "project-memory")
	if err != nil || !slices.Equal(got, []string{"project-memory/wiki/p.md"}) {
		t.Fatalf("%v %v", got, err)
	}
}

func TestObjectFormatAndFullResolution(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	r, _ := Discover(ctx, dir)
	if f, err := r.ObjectFormat(ctx); err != nil || f != "sha1" {
		t.Fatalf("format %q %v", f, err)
	}
	full := gitT(t, dir, "rev-parse", "HEAD:a.txt")
	got, err := r.ResolveObject(ctx, full[:10], "blob")
	if err != nil || got != full {
		t.Fatalf("short id must resolve to the full id: %q %v", got, err)
	}
	if _, err := r.ResolveObject(ctx, "0123456789abcdef0123", "blob"); !errors.Is(err, ErrUnknownObject) {
		t.Fatalf("an absent object: want ErrUnknownObject, got %v", err)
	}
}

// Two thousand blobs make a shared 4-character prefix all but certain (birthday bound).
func TestAmbiguousShortIDIsAnError(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	var paths []string
	for i := 0; i < 2000; i++ {
		p := filepath.Join(dir, "blobs", fmt.Sprintf("b%04d", i))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(fmt.Sprintf("blob %d\n", i)), 0o644)
		paths = append(paths, p)
	}
	c := exec.Command("git", "-C", dir, "hash-object", "-w", "--stdin-paths")
	c.Stdin = strings.NewReader(strings.Join(paths, "\n") + "\n")
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	byPrefix := map[string]int{}
	prefix := ""
	for _, oid := range strings.Fields(string(out)) {
		byPrefix[oid[:4]]++
		if byPrefix[oid[:4]] == 2 {
			prefix = oid[:4]
			break
		}
	}
	if prefix == "" {
		t.Fatal("no shared prefix among 2000 blobs; the fixture is not strong enough")
	}
	r, _ := Discover(ctx, dir)
	if got, err := r.ResolveObject(ctx, prefix, "blob"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("an ambiguous prefix must be an error, got %q %v", got, err)
	}
}

// No Wikory command needs a filter or diff driver, so none of them can observably run one;
// this checks the defence itself: every driver the repository names is overridden to empty.
func TestDriversAreNeutralised(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	gitT(t, dir, "config", "filter.evil.clean", "touch PWNED")
	gitT(t, dir, "config", "filter.evil.required", "true")
	gitT(t, dir, "config", "diff.evil.textconv", "touch PWNED")
	r, err := Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range r.neutralise {
		got[o[0]] = o[1]
	}
	want := map[string]string{"filter.evil.clean": "", "filter.evil.smudge": "", "filter.evil.process": "", "filter.evil.required": "false",
		"diff.evil.textconv": "", "diff.evil.command": ""}
	for k, v := range want {
		if gv, ok := got[k]; !ok || gv != v {
			t.Errorf("missing override %q=%q in %v", k, v, r.neutralise)
		}
	}
}

// The config read that builds the override list must fail closed: a Repo returned without
// its overrides would run whatever the repository names. Exit status 1 with an empty stderr
// is git's "no matching keys" and means an empty list; every other failure is an error,
// including exit 1 with warnings (an unreadable config file reads as "no keys").
func TestDriverDiscoveryFailsClosed(t *testing.T) {
	ctx := context.Background()
	t.Run("no keys is an empty list", func(t *testing.T) {
		got, err := driverOverrides(ctx, newRepo(t))
		if err != nil || len(got) != 0 {
			t.Fatalf("want an empty list and no error, got %v %v", got, err)
		}
	})
	t.Run("corrupt config", func(t *testing.T) {
		dir := newRepo(t)
		f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString("[broken\n")
		_ = f.Close()
		if got, err := driverOverrides(ctx, dir); err == nil {
			t.Fatalf("a corrupt config returned no error: %v", got)
		}
	})
	t.Run("config is a directory", func(t *testing.T) {
		dir := newRepo(t)
		cfg := filepath.Join(dir, ".git", "config")
		if err := os.Remove(cfg); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(cfg, 0o755); err != nil {
			t.Fatal(err)
		}
		if got, err := driverOverrides(ctx, dir); err == nil {
			t.Fatalf("an unreadable config returned no error: %v", got)
		}
	})
}

// The isolation in TestMain must not hide the behaviour it isolates from: a driver named in
// the user's global config is read and neutralised exactly like one in the repository's own.
func TestGlobalDriversAreNeutralised(t *testing.T) {
	cfg := "[filter \"evil\"]\n\tclean = touch PWNED\n\trequired = true\n"
	if err := os.WriteFile(isolatedGlobalConfig, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(isolatedGlobalConfig, nil, 0o600); err != nil {
			t.Error(err)
		}
	})
	r, err := Discover(context.Background(), newRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range r.neutralise {
		got[o[0]] = o[1]
	}
	if v, ok := got["filter.evil.clean"]; !ok || v != "" {
		t.Errorf("the global filter.evil.clean is not neutralised: %v", r.neutralise)
	}
	if v, ok := got["filter.evil.required"]; !ok || v != "false" {
		t.Errorf("the global filter.evil.required is not set to false: %v", r.neutralise)
	}
}

// git refuses a corrupt config before the override read is reached, so this end-to-end check
// passes with or without the fail-closed read; TestDriverDiscoveryFailsClosed is the guard.
func TestDiscoverRefusesCorruptConfig(t *testing.T) {
	dir := newRepo(t)
	f, err := os.OpenFile(filepath.Join(dir, ".git", "config"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("[broken\n")
	_ = f.Close()
	if r, err := Discover(context.Background(), dir); err == nil || r != nil {
		t.Fatalf("a corrupt config must give an error and no Repo, got %+v %v", r, err)
	}
}
