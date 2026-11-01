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
