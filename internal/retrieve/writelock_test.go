package retrieve

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/store"
)

// The refresh transaction is a database write, so with a WriteLockPath it runs inside the
// cross-process write lock: held by another writer, the refresh fails with ErrWriteLockTimeout
// and indexes nothing; once released the same refresh works.
func TestRefreshTakesTheWriteLock(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	writePage(t, repo, "queue.md", queuePage)
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo), WriteLockPath: st.WriteLockPath()}
	u, err := store.Lock(st.WriteLockPath(), 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, err = ix.Refresh(ctx)
	if !errors.Is(err, store.ErrWriteLockTimeout) {
		t.Fatalf("Refresh with db-write.lock held: want ErrWriteLockTimeout, got %v", err)
	}
	if n := ftsCount(t, st, "co-1", "project-memory/wiki/queue.md"); n != 0 {
		t.Fatalf("the refused refresh indexed %d row(s)", n)
	}
	u()
	s, err := ix.Refresh(context.Background())
	if err != nil || s.Changed != 1 {
		t.Fatalf("refresh after release: %+v %v", s, err)
	}
}
