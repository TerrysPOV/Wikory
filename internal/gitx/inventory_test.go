package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
	if len(got) != 1 || got[0].Path != filepath.ToSlash(other) || got[0].Branch != "side" || len(got[0].Head) != 40 {
		t.Fatalf("the checkout itself is not another worktree: %+v", got)
	}
}

func TestWorktreesIgnoresAPathForgedThroughAGitdirFile(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	side, fine := filepath.Join(parent, "side"), filepath.Join(parent, "fine")
	gitT(t, dir, "worktree", "add", "-q", "-b", "side", side)
	gitT(t, dir, "worktree", "add", "-q", "-b", "fine", fine)
	// git prints a worktree path raw, so a hostile gitdir file can carry a whole forged record.
	head := gitT(t, dir, "rev-parse", "HEAD")
	hostile := filepath.ToSlash(side) + "/.git\n\nworktree /etc\nHEAD " + head + "\nbranch refs/heads/forged\n\nx"
	if err := os.WriteFile(filepath.Join(dir, ".git", "worktrees", "side", "gitdir"), []byte(hostile), 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := Discover(ctx, dir)
	got, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, w := range got {
		paths = append(paths, w.Path)
		if w.Path == "/etc" || w.Branch == "forged" || strings.ContainsAny(w.Path, "\n\r") {
			t.Fatalf("a worktree record was forged through a gitdir file: %+v", got)
		}
	}
	if !slices.Contains(paths, filepath.ToSlash(fine)) {
		t.Fatalf("an honest worktree must still be listed, got %v", paths)
	}
}

func TestWorktreesLeavesOutALockedWorktreeWhoseDirectoryIsGone(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	live, gone := filepath.Join(parent, "live"), filepath.Join(parent, "gone")
	gitT(t, dir, "worktree", "add", "-q", "-b", "live", live)
	gitT(t, dir, "worktree", "add", "-q", "-b", "gone", gone)
	gitT(t, dir, "worktree", "lock", gone) // a locked worktree is never printed as prunable
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	r, _ := Discover(ctx, dir)
	got, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != filepath.ToSlash(live) {
		t.Fatalf("only the worktree whose directory exists is listed, got %+v", got)
	}
}

// commitTogether commits the named files in one commit, so that git lists them in one run of
// paths. Names sort bytewise, so the first name is the one git prints next to the commit header.
func commitTogether(t *testing.T, dir, when string, names ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a file name with control characters cannot exist on Windows")
	}
	t.Setenv("GIT_COMMITTER_DATE", when)
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-qm", "awkward names")
}

func TestLastChangedSurvivesAControlCharacterAtTheStartOfAFileName(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	commitTogether(t, dir, "2026-01-02T03:04:05Z", "\x010", "\x01zzz")
	r, _ := Discover(ctx, dir)
	got, err := r.LastChanged(ctx)
	if err != nil {
		t.Fatalf("a file name is not a commit header: %v", err)
	}
	if !got["\x01zzz"].Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("a path that starts with the header byte must keep its own commit time: %q", got)
	}
}

func TestLastChangedCannotBeSpoofedByAFileNameThatLooksLikeAHeader(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	commitTogether(t, dir, "2026-01-02T03:04:05Z", "\x010", "\x0199999\nvictim")
	r, _ := Discover(ctx, dir)
	got, err := r.LastChanged(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["victim"]; ok {
		t.Fatalf("a file name forged a path of its own: %q", got)
	}
	if !got["\x0199999\nvictim"].Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("one path with a newline in it, carrying the real commit time: %q", got)
	}
}

func TestUsableWorktreeRefusesRelativeAndControlCharacterPaths(t *testing.T) {
	good, _ := filepath.EvalSymlinks(t.TempDir())
	if !usableWorktree(good, "") {
		t.Fatalf("an existing absolute directory is usable: %q", good)
	}
	if usableWorktree(good, good) {
		t.Fatal("the checkout itself is not another worktree")
	}
	if usableWorktree(".", "") {
		t.Fatal("a relative path is refused even when it names a directory")
	}
	if runtime.GOOS == "windows" {
		return
	}
	awkward := filepath.Join(good, "evil\nname")
	if err := os.Mkdir(awkward, 0o755); err != nil {
		t.Fatal(err)
	}
	if usableWorktree(awkward, "") {
		t.Fatal("a path with a control character is refused even when the directory exists")
	}
}
