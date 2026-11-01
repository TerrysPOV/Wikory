package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// core.worktree in .git/config makes git report another directory as the work tree. Wikory
// writes inside the work tree, so Discover must refuse a work tree that does not contain the
// directory it was asked about.
func TestCoreWorktreeOutsideTheCurrentDirectoryIsRefused(t *testing.T) {
	dir := newRepo(t)
	sibling, _ := filepath.EvalSymlinks(t.TempDir())
	gitT(t, dir, "config", "core.worktree", sibling)
	r, err := Discover(context.Background(), dir)
	if !errors.Is(err, ErrWorktreeRedirected) {
		t.Fatalf("want ErrWorktreeRedirected, got repo=%+v err=%v", r, err)
	}
}

func TestDiscoverFromASubdirectoryStillWorks(t *testing.T) {
	dir := newRepo(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Discover(context.Background(), sub)
	if err != nil || r.Top != dir || !r.Confirmed {
		t.Fatalf("top=%v confirmed=%v err=%v", r, r != nil && r.Confirmed, err)
	}
}

// Only git can say that a directory is a worktree of a clone: the main work tree is the
// parent of the common dir, and a linked one is listed by `git worktree list`. A directory
// whose .git file merely names another clone's git dir is neither.
func TestConfirmedOnlyForWorktreesGitListsItself(t *testing.T) {
	ctx := context.Background()
	main := newRepo(t)
	linked := main + "-wt"
	gitT(t, main, "worktree", "add", "-q", linked)
	t.Cleanup(func() { os.RemoveAll(linked) })
	linked, _ = filepath.EvalSymlinks(linked)
	hostile, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(hostile, ".git"), []byte("gitdir: "+filepath.Join(main, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hostileLinked, _ := filepath.EvalSymlinks(t.TempDir())
	ids, _ := filepath.Glob(filepath.Join(main, ".git", "worktrees", "*"))
	if len(ids) != 1 {
		t.Fatalf("worktree ids %v", ids)
	}
	if err := os.WriteFile(filepath.Join(hostileLinked, ".git"), []byte("gitdir: "+ids[0]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		dir  string
		want bool
	}{{"main", main, true}, {"linked", linked, true}, {"hostile main", hostile, false}, {"hostile linked", hostileLinked, false}} {
		r, err := Discover(ctx, c.dir)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if r.Confirmed != c.want {
			t.Errorf("%s: Confirmed=%v, want %v (top=%s common=%s)", c.name, r.Confirmed, c.want, r.Top, r.CommonDir)
		}
	}
}

// An unborn HEAD is a state, not a failure: the empty id with no error. A failed git call is
// an error, so "" == "" can never pass for two failed reads.
func TestHeadReadsDistinguishUnbornFromFailure(t *testing.T) {
	ctx := context.Background()
	fresh, _ := filepath.EvalSymlinks(t.TempDir())
	gitT(t, fresh, "-c", "init.defaultBranch=main", "init", "-q")
	r, err := Discover(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := r.HeadCommit(ctx); c != "" || err != nil {
		t.Fatalf("unborn HeadCommit = %q, %v; want empty and nil", c, err)
	}
	if tr, err := r.HeadTree(ctx); tr != "" || err != nil {
		t.Fatalf("unborn HeadTree = %q, %v; want empty and nil", tr, err)
	}
	// HEAD names a branch whose commit object is missing: git cannot answer.
	dir := newRepo(t)
	r, err = Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	ref := filepath.Join(dir, ".git", "refs", "heads", "main")
	if err := os.WriteFile(ref, []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := r.HeadCommit(ctx); err == nil {
		t.Fatalf("a HEAD whose commit is missing read as %q with no error", c)
	}
	if tr, err := r.HeadTree(ctx); err == nil {
		t.Fatalf("a HEAD whose commit is missing read as tree %q with no error", tr)
	}
	// The directory vanishing mid-run makes every git call fail.
	gone := newRepo(t)
	r, _ = Discover(ctx, gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := r.HeadTree(ctx); err == nil {
		t.Fatal("HeadTree swallowed a git failure")
	}
	if _, err := r.HeadBlobs(ctx, []string{"a.txt"}); err == nil {
		t.Fatal("HeadBlobs swallowed a git failure")
	}
}
