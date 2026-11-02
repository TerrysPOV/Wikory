package capture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/decode"
	"github.com/TerrysPOV/Wikory/internal/store"
)

// holdWriteLock takes the store's database write lock as another writer would. flock and
// LockFileEx both conflict between two handles, so holding it here stands in for another
// process on every platform.
func holdWriteLock(t *testing.T, r *rig) func() {
	t.Helper()
	u, err := store.Lock(r.rec.Store.WriteLockPath(), 0)
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

func countRows(t *testing.T, r *rig, table string) int {
	t.Helper()
	var n int
	if err := r.rec.Store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Every capture write goes through the database write lock. With another writer holding it,
// each fails with ErrWriteLockTimeout and writes nothing; when it is released the same call works.
func TestTurnStartTakesTheWriteLock(t *testing.T) {
	r := newRig(t)
	release := holdWriteLock(t, r)
	err := r.rec.TurnStart(shortCtx(t), r.ev(decode.UserPrompt, "x", "p1", ""))
	if !errors.Is(err, store.ErrWriteLockTimeout) {
		t.Fatalf("TurnStart with db-write.lock held: want ErrWriteLockTimeout, got %v", err)
	}
	if n := countRows(t, r, "turn_starts"); n != 0 {
		t.Fatalf("the refused TurnStart wrote %d row(s)", n)
	}
	release()
	if err := r.rec.TurnStart(context.Background(), r.ev(decode.UserPrompt, "x", "p1", "")); err != nil {
		t.Fatal(err)
	}
}

func TestStopTakesTheWriteLockAndIsRepairedOnRetry(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	release := holdWriteLock(t, r)
	_, _, err := r.rec.Stop(shortCtx(t), r.ev(decode.Stop, "", "p1", "answer"))
	if !errors.Is(err, store.ErrWriteLockTimeout) {
		t.Fatalf("Stop with db-write.lock held: want ErrWriteLockTimeout, got %v", err)
	}
	if n := countRows(t, r, "turns"); n != 0 {
		t.Fatalf("the refused Stop wrote %d turn row(s)", n)
	}
	release()
	// The version file was already on disk; the retry adopts it and inserts the row.
	if _, _, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "answer")); err != nil {
		t.Fatalf("retry after the lock was released: %v", err)
	}
	if n := countRows(t, r, "turns"); n != 1 {
		t.Fatalf("the retry left %d turn rows, want 1", n)
	}
}

func TestSealTransitionTakesTheWriteLock(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	id, _, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "never in the transcript"))
	if err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(11 * time.Minute) // past the seal timeout, so Seal will write version 2
	release := holdWriteLock(t, r)
	n, err := r.rec.Seal(shortCtx(t), r.farDeadline())
	if !errors.Is(err, store.ErrWriteLockTimeout) || n != 0 {
		t.Fatalf("Seal with db-write.lock held: want ErrWriteLockTimeout and 0 sealed, got n=%d err=%v", n, err)
	}
	if state, version, _, _ := r.row(t, id); state != "captured" || version != 1 {
		t.Fatalf("the refused seal moved the row to %s v%d", state, version)
	}
	release()
	if n, err := r.rec.Seal(ctx, r.farDeadline()); err != nil || n != 1 {
		t.Fatalf("seal after release: n=%d err=%v", n, err)
	}
}

func TestSweepStaleTakesTheWriteLock(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	ev := r.ev(decode.UserPrompt, "x", "m1", "")
	ev.SessionID = "other"
	if err := r.rec.TurnStart(ctx, ev); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(7 * time.Hour) // past StaleMarkerAfter
	release := holdWriteLock(t, r)
	err := r.rec.SweepStale(shortCtx(t), "sess-1")
	if !errors.Is(err, store.ErrWriteLockTimeout) {
		t.Fatalf("SweepStale with db-write.lock held: want ErrWriteLockTimeout, got %v", err)
	}
	if n := countRows(t, r, "turns"); n != 0 {
		t.Fatalf("the refused sweep wrote %d turn row(s)", n)
	}
	release()
	if err := r.rec.SweepStale(ctx, "sess-1"); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, r, "turns"); n != 1 {
		t.Fatalf("the sweep after release left %d turn rows, want 1", n)
	}
}
