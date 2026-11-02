package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Review L1: a version-1 database that lacks one expected object used to be repaired by the
// "up to date" path without the write lock. The repair writes, so it must take the lock; a
// complete database still opens without it.
func TestOpeningADatabaseMissingAnObjectTakesTheWriteLock(t *testing.T) {
	data := t.TempDir()
	id := ident("wkp_S", "github.com/o/s", "/s/.git", "/s")
	s := open(t, data, id, true)
	dir, lock := s.Dir, s.WriteLockPath()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Remove one index from a scratch database, as an interrupted upgrade might leave it.
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "wikory.db")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("DR" + "OP INDEX turns_by_checkout"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	release := holdWriteLockInAnotherProcess(t, lock)
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond*slack())
	_, err = Resolve(ctx, data, id, false)
	cancel()
	if !errors.Is(err, ErrWriteLockTimeout) {
		t.Fatalf("repairing a missing index while another process held db-write.lock: want ErrWriteLockTimeout, got %v", err)
	}
	release()

	s2 := open(t, data, id, false)
	var n int
	if err := s2.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'turns_by_checkout'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the missing index was not recreated: n=%d err=%v", n, err)
	}
	s2.Close()
	// A complete database opens without waiting for the lock.
	release = holdWriteLockInAnotherProcess(t, lock)
	defer release()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 800*time.Millisecond*slack())
	defer cancel2()
	s3, err := Resolve(ctx2, data, id, false)
	if err != nil {
		t.Fatalf("a complete database should open while another process holds the lock: %v", err)
	}
	s3.Close()
}
