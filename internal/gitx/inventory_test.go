package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func commitFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "add", "--", rel)
	gitT(t, dir, "commit", "-qm", "edit "+rel)
}

func TestListTreeReturnsRegularBlobsWithSizes(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	commitFile(t, dir, "pkg/b c.go", "package b\n")
	gitT(t, dir, "update-index", "--add", "--cacheinfo", "120000,"+gitT(t, dir, "rev-parse", "HEAD:a.txt")+",link")
	gitT(t, dir, "commit", "-qm", "symlink")
	r, _ := Discover(ctx, dir)
	got, err := r.ListTree(ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]TreeEntry{}
	for _, e := range got {
		byPath[e.Path] = e
	}
	if e, ok := byPath["pkg/b c.go"]; !ok || e.Size != int64(len("package b\n")) || e.OID == "" {
		t.Fatalf("missing or wrong entry for a path with a space: %+v", got)
	}
	if _, ok := byPath["link"]; ok {
		t.Fatal("a symlink must not be listed as a readable blob")
	}
	if _, ok := byPath["a.txt"]; !ok {
		t.Fatal("a.txt missing")
	}
}

func TestListTreeAcceptsOnlyHeadOrAHexCommitID(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	r, _ := Discover(ctx, dir)
	// A valid tree-ish that is neither HEAD nor a commit id: git would list it, Wikory must not.
	if _, err := r.ListTree(ctx, "HEAD^{tree}"); err == nil {
		t.Fatal("a revision that is neither HEAD nor a hex commit id must be refused")
	}
	if _, err := r.ListTree(ctx, "--name-only"); err == nil {
		t.Fatal("an option-shaped revision must be refused")
	}
	if es, err := r.ListTree(ctx, gitT(t, dir, "rev-parse", "HEAD")); err != nil || len(es) == 0 {
		t.Fatalf("a full commit id is accepted: %v %v", es, err)
	}
}

func TestReadBlobHonoursTheLimitAndRefusesNonIDs(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	r, _ := Discover(ctx, dir)
	oid := gitT(t, dir, "rev-parse", "HEAD:a.txt")
	b, err := r.ReadBlob(ctx, oid, 100)
	if err != nil || string(b) != "alpha\n" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := r.ReadBlob(ctx, oid, 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a blob of 6 bytes against a limit of 5 must be ErrTooLarge, got %v", err)
	}
	if _, err := r.ReadBlob(ctx, "HEAD:a.txt", 100); err == nil {
		t.Fatal("only a full hex id is accepted")
	}
}

func TestChurnCountsCommitsPerPathIncludingAwkwardNames(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	commitFile(t, dir, "a.txt", "one\n")
	commitFile(t, dir, "a.txt", "two\n")
	commitFile(t, dir, "dir/b c.txt", "x\n")
	commitFile(t, dir, "café.txt", "x\n")
	r, _ := Discover(ctx, dir)
	got, err := r.Churn(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got["a.txt"] != 3 || got["dir/b c.txt"] != 1 || got["café.txt"] != 1 {
		t.Fatalf("churn = %v", got)
	}
	old, err := r.Churn(ctx, time.Now().Add(24*time.Hour))
	if err != nil || len(old) != 0 {
		t.Fatalf("a since-date in the future must count nothing: %v %v", old, err)
	}
}

func TestDefaultBranchFallsBackToMainThenMaster(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	r, _ := Discover(ctx, dir)
	want := r.Branch(ctx)
	if !slices.Contains([]string{"main", "master"}, want) {
		t.Skipf("fixture branch is %q", want)
	}
	if got := r.DefaultBranch(ctx); got != want {
		t.Fatalf("DefaultBranch = %q, want %q", got, want)
	}
}

func TestLastChangedIsTheNewestCommitTimePerPath(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-02T03:04:05Z")
	commitFile(t, dir, "a.txt", "two\n")
	t.Setenv("GIT_COMMITTER_DATE", "2026-02-03T04:05:06Z")
	commitFile(t, dir, "dir/b c.txt", "x\n")
	commitFile(t, dir, "café.txt", "x\n")
	r, _ := Discover(ctx, dir)
	got, err := r.LastChanged(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	b := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if !got["a.txt"].Equal(a) {
		t.Fatalf("a.txt was last changed by the second commit (%v), got %v: the first sighting in the log is the newest", a, got["a.txt"])
	}
	if !got["dir/b c.txt"].Equal(b) || !got["café.txt"].Equal(b) {
		t.Fatalf("awkward names must survive: %v", got)
	}
}

func TestBranchesListsOnlyLocalBranchesWithCommitsHeadLacks(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	gitT(t, dir, "branch", "merged") // same commit as main: nothing unmerged
	gitT(t, dir, "checkout", "-q", "-b", "feature/x")
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-02T03:04:05Z")
	commitFile(t, dir, "x.txt", "x\n")
	commitFile(t, dir, "y.txt", "y\n")
	gitT(t, dir, "checkout", "-q", "main")
	r, _ := Discover(ctx, dir)
	got, err := r.Branches(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "feature/x" || got[0].Ahead != 2 || !got[0].Tip.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("only feature/x holds commits that HEAD lacks (two of them): %+v", got)
	}
}

func TestWorktreesListsTheOtherWorkingTreesNotThisOne(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	other := filepath.Join(parent, "side")
	gitT(t, dir, "worktree", "add", "-q", "-b", "side", other)
	r, _ := Discover(ctx, dir)
	got, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != other || got[0].Branch != "side" || len(got[0].Head) != 40 {
		t.Fatalf("the checkout itself is not another worktree: %+v", got)
	}
}
