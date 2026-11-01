//go:build unix

package retrieve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshRefusesSymlinkedPagesAndDirs(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	repo := t.TempDir()
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "secret.md"), []byte("# Secret\n\nOUTSIDE-SECRET-MARKER\n"), 0o644)
	writePage(t, repo, "queue.md", queuePage)
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(repo, "project-memory", "wiki", "linked.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "project-memory", "wiki", "linkeddir")); err != nil {
		t.Fatal(err)
	}
	s, err := (&Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}).Refresh(ctx)
	if err != nil || s.Refused != 2 || s.Pages != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	var n int
	_ = st.DB.QueryRow(`SELECT count(*) FROM fts WHERE fts MATCH '"marker"'`).Scan(&n)
	if n != 0 {
		t.Fatal("content behind a symlink was indexed")
	}
}

// One hostile or broken entry must not stop the rest of the wiki being indexed.
func TestRefreshSurvivesBadSubdirectories(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 000 does not stop root")
	}
	ctx := context.Background()
	st := newStore(t)
	repo := t.TempDir()
	outside := t.TempDir()
	writePage(t, repo, "queue.md", queuePage)
	writePage(t, repo, "locked/hidden.md", "# Hidden\n\nUnreadable.\n")
	writePage(t, repo, "zz/after.md", "# After\n\nSorts after the bad entries.\n")
	locked := filepath.Join(repo, "project-memory", "wiki", "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if err := os.Symlink(outside, filepath.Join(repo, "project-memory", "wiki", "linkeddir")); err != nil {
		t.Fatal(err)
	}
	s, err := (&Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}).Refresh(ctx)
	if err != nil {
		t.Fatalf("one bad entry aborted the refresh: %v", err)
	}
	if s.Pages != 2 || s.Refused != 2 {
		t.Fatalf("%+v", s)
	}
	if ftsCount(t, st, "co-1", "project-memory/wiki/zz/after.md") == 0 {
		t.Fatal("a page after the bad entries was not indexed")
	}
}
