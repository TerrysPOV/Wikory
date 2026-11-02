package gitx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Review L1: `git worktree list --porcelain` separates fields with newlines, so a linked
// worktree whose own directory name holds a newline can print a line that reads as another
// worktree. A hostile directory named in that forged line used to be Confirmed, and so
// inherited the trusted clone's trust.
func TestForgedWorktreeLineDoesNotConfirmAHostileDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows directory name cannot hold a newline")
	}
	ctx := context.Background()
	main := newRepo(t)
	hostile, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(hostile, ".git"), []byte("gitdir: "+filepath.Join(main, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	forging := main + "-wt\nworktree " + hostile
	gitT(t, main, "worktree", "add", "-q", forging)
	t.Cleanup(func() { os.RemoveAll(forging) })
	r, err := Discover(ctx, hostile)
	if err != nil {
		t.Fatal(err)
	}
	if r.Confirmed {
		t.Fatalf("a directory named only by a forged worktree line was confirmed (top=%s common=%s)", r.Top, r.CommonDir)
	}
}

// A linked worktree that git lists but whose .git file has been pointed away from its entry
// under <common>/worktrees is not vouched for.
func TestListedWorktreeWhoseGitFileIsRepointedIsNotConfirmed(t *testing.T) {
	ctx := context.Background()
	main := newRepo(t)
	linked := main + "-wt"
	gitT(t, main, "worktree", "add", "-q", linked)
	t.Cleanup(func() { os.RemoveAll(linked) })
	linked, _ = filepath.EvalSymlinks(linked)
	if r, err := Discover(ctx, linked); err != nil || !r.Confirmed {
		t.Fatalf("a genuine linked worktree must be confirmed first: %v %v", r, err)
	}
	if err := os.WriteFile(filepath.Join(linked, ".git"), []byte("gitdir: "+filepath.Join(main, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Discover(ctx, linked)
	if err != nil {
		t.Fatal(err)
	}
	if r.Confirmed {
		t.Fatalf("a worktree whose .git file no longer names its own entry was confirmed (gitdir=%s)", r.GitDir)
	}
}

// The NUL-separated form keeps a directory name that holds a newline in one field.
func TestListWorktreesKeepsANewlineNamedDirectoryInOneEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a Windows directory name cannot hold a newline")
	}
	main := newRepo(t)
	forging := main + "-wt\nworktree /somewhere/else"
	gitT(t, main, "worktree", "add", "-q", forging)
	t.Cleanup(func() { os.RemoveAll(forging) })
	got, err := listWorktrees(context.Background(), filepath.Join(main, ".git"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want exactly 2 entries (main and the newline-named one), got %d: %q", len(got), got)
	}
	for _, p := range got {
		if p == "/somewhere/else" {
			t.Fatalf("a forged entry appeared: %q", got)
		}
	}
}

func TestParseWorktreesSkipsPrunableEntries(t *testing.T) {
	out := "worktree /a\x00HEAD 1\x00branch refs/heads/main\x00\x00" +
		"worktree /gone\x00HEAD 2\x00detached\x00prunable gitdir file points to non-existent location\x00\x00" +
		"worktree /b\x00HEAD 3\x00locked\x00\x00" +
		"worktree /bare-prunable\x00prunable\x00\x00"
	got := parseWorktrees([]byte(out))
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("got %q, want [/a /b]", got)
	}
}

func TestGitReallyMarksARemovedWorktreePrunable(t *testing.T) {
	main := newRepo(t)
	linked := main + "-wt"
	gitT(t, main, "worktree", "add", "-q", linked)
	if err := os.RemoveAll(linked); err != nil {
		t.Fatal(err)
	}
	got, err := listWorktrees(context.Background(), filepath.Join(main, ".git"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("a worktree whose directory is gone was still listed: %q", got)
	}
}

// worktreeBound on hand-built directories: the entry's gitdir file must name top/.git.
func TestWorktreeBoundNeedsBothFilesToAgree(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	common := filepath.Join(base, "clone.git")
	entry := filepath.Join(common, "worktrees", "wt1")
	top := filepath.Join(base, "top")
	other := filepath.Join(base, "other")
	for _, d := range []string{entry, top, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(top, ".git"), "gitdir: "+entry+"\n")
	write(filepath.Join(other, ".git"), "x")
	write(filepath.Join(entry, "gitdir"), filepath.Join(top, ".git")+"\n")
	if !worktreeBound(top, common, entry) {
		t.Fatal("two agreeing files were not accepted")
	}
	write(filepath.Join(entry, "gitdir"), filepath.Join(other, ".git")+"\n")
	if worktreeBound(top, common, entry) {
		t.Error("an entry whose gitdir file names another directory was accepted")
	}
	write(filepath.Join(entry, "gitdir"), filepath.Join(top, ".git")+"\n")
	write(filepath.Join(top, ".git"), "gitdir: "+filepath.Join(common, "worktrees")+"\n")
	if worktreeBound(top, common, entry) {
		t.Error("a .git file naming a different directory was accepted")
	}
	// gitDir not directly under <common>/worktrees.
	write(filepath.Join(top, ".git"), "gitdir: "+other+"\n")
	write(filepath.Join(other, "gitdir"), filepath.Join(top, ".git")+"\n")
	if worktreeBound(top, common, other) {
		t.Error("a git directory outside <common>/worktrees was accepted")
	}
}
