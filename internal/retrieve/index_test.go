package retrieve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRefreshIndexesUpdatesAndRemoves(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	repo := t.TempDir()
	writePage(t, repo, "queue.md", queuePage)
	writePage(t, repo, "other.md", "# Other\n\nPlain prose.\n")
	writePage(t, repo, "_archive/old.md", "# Old\n\nArchived.\n")
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}
	s, err := ix.Refresh(ctx)
	if err != nil || s.Pages != 2 || s.Changed != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if ftsCount(t, st, "co-1", "project-memory/wiki/_archive/old.md") != 0 {
		t.Fatal("archive was indexed")
	}
	s, _ = ix.Refresh(ctx)
	if s.Changed != 0 {
		t.Fatalf("an unchanged tree reindexed: %+v", s)
	}
	if err := os.Remove(filepath.Join(repo, "project-memory", "wiki", "other.md")); err != nil {
		t.Fatal(err)
	}
	s, _ = ix.Refresh(ctx)
	if s.Removed != 1 || ftsCount(t, st, "co-1", "project-memory/wiki/other.md") != 0 {
		t.Fatalf("a removed page stayed in the index: %+v", s)
	}
}

func TestRefreshIsPerCheckout(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	a, b := t.TempDir(), t.TempDir()
	writePage(t, a, "queue.md", queuePage)
	_, _ = (&Indexer{DB: st.DB, CheckoutID: "co-A", Repo: openRoot(t, a)}).Refresh(ctx)
	_, _ = (&Indexer{DB: st.DB, CheckoutID: "co-B", Repo: openRoot(t, b)}).Refresh(ctx)
	if ftsCount(t, st, "co-A", "project-memory/wiki/queue.md") == 0 || ftsCount(t, st, "co-B", "project-memory/wiki/queue.md") != 0 {
		t.Fatal("index rows leaked between checkouts")
	}
}
