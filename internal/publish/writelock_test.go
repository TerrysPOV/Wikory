package publish

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/store"
)

func holdWriteLock(t *testing.T, f *fixture) func() {
	t.Helper()
	u, err := store.Lock(f.st.WriteLockPath(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var done bool
	release := func() {
		if !done {
			done = true
			u()
		}
	}
	t.Cleanup(release)
	return release
}

func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

// The publisher keeps its two lock files side by side, so it finds the write lock from the
// publish lock's path; that must be the store's own write lock.
func TestPublisherUsesTheStoresWriteLockFile(t *testing.T) {
	f := newFixture(t)
	p := f.publisher("co-1")
	if got, want := p.writeLockPath(), f.st.WriteLockPath(); got != want {
		t.Fatalf("publisher write lock %s, store write lock %s", got, want)
	}
	if filepath.Base(p.LockPath) == filepath.Base(p.writeLockPath()) {
		t.Fatal("the write lock and the publish lock are the same file")
	}
}

// Every database write of the publisher takes the write lock: with another writer holding it,
// each fails with ErrWriteLockTimeout and changes nothing.
func TestEveryPublishDatabaseWriteTakesTheWriteLock(t *testing.T) {
	f := newFixture(t)
	p := f.publisher("co-1")
	j, err := p.Begin(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	m := &manifest{JobID: j.ID, Epoch: j.Epoch, Ops: []manifestOp{{Target: "project-memory/wiki/a.md", Old: Absent, New: "x"}}}
	writes := map[string]func(ctx context.Context) error{
		"Begin":     func(ctx context.Context) error { _, err := p.Begin(ctx, "test"); return err },
		"Takeover":  func(ctx context.Context) error { _, err := p.Takeover(ctx, j.ID); return err },
		"setJob":    func(ctx context.Context) error { return p.setJob(ctx, j.ID, "refused", "x") },
		"recordOps": func(ctx context.Context) error { return p.recordOps(ctx, m) },
		"markDone":  func(ctx context.Context) error { return p.markDone(ctx, m, 0) },
		"finishJob": func(ctx context.Context) error { return p.finishJob(ctx, m) },
	}
	release := holdWriteLock(t, f)
	for name, w := range writes {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		err := w(ctx)
		cancel()
		if !errors.Is(err, store.ErrWriteLockTimeout) {
			t.Errorf("%s did not take db-write.lock: want ErrWriteLockTimeout, got %v", name, err)
		}
	}
	release()
	if st := f.jobState(t, j.ID); st != "begun" {
		t.Fatalf("a refused write changed the job to %q", st)
	}
	var n int
	if err := f.st.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("jobs = %d err=%v, want the single job from the setup", n, err)
	}
}

// Lock order is publish.lock -> db-write.lock: Takeover holds the first and takes the second
// for its write, and a whole publish does not deadlock on itself.
func TestPublishHoldsThePublishLockOutsideTheWriteLock(t *testing.T) {
	f := newFixture(t)
	p := f.publisher("co-1")
	j, err := p.Begin(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Takeover(context.Background(), j.ID); err != nil {
		t.Fatalf("Takeover (publish.lock then db-write.lock): %v", err)
	}
	if err := p.Publish(context.Background(), Job{ID: j.ID, CheckoutID: j.CheckoutID, Kind: j.Kind, Fingerprint: j.Fingerprint, Epoch: j.Epoch + 1}, standardOps(), "x"); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}
