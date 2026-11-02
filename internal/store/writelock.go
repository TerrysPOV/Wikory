package store

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

// The database write lock.
//
// SQLite's own locking is not enough between processes on Windows: eight processes each
// running 200 immediate transactions on one WAL database lost writes there (CI run
// 41370903722: failed=4, rows=800/1600), and the same workload serialised by an OS file lock
// lost none. Hooks of different sessions run as parallel processes, so every database WRITE
// transaction in production code runs inside WithWriteLock, on every platform.
//
// Lock order, outermost first: registry.lock -> publish.lock -> db-write.lock (clone-id.lock,
// taken only by clone setup, sits outside db-write.lock as well and is never taken while
// publish.lock is held). db-write.lock is always the innermost lock and is held only for the
// write transaction itself, never across a wait for another lock, so no cycle exists. The lock is not re-entrant: fn must not call
// WithWriteLock again (directly or through a Store write method). A nested call made with the
// context fn was given returns ErrWriteLockReentered instead of deadlocking.

// writeLockName is the lock file under the store's locks directory. It is separate from
// publish.lock and registry.lock so that taking it never re-enters either.
const writeLockName = "db-write.lock"

const (
	// DefaultWriteLockWait caps how long a write waits for another process's write.
	DefaultWriteLockWait = 3 * time.Second
	// writeLockDeadlineShare is the part of the time left before the caller's deadline that a
	// wait may use, so a caller that is out of time still has some left to report the failure.
	writeLockDeadlineShare = 0.6
)

var (
	// ErrWriteLockTimeout means another process held the database write lock for the whole
	// wait. Callers treat it as a failed write; hooks log kind "dblock" and carry on.
	ErrWriteLockTimeout = errors.New("store: database write lock not acquired in time")
	// ErrWriteLockReentered means WithWriteLock was called while the caller already held the
	// lock. It is a programming error, reported instead of deadlocking.
	ErrWriteLockReentered = errors.New("store: WithWriteLock called while the write lock is already held")
)

// heldKey marks a context as running inside the write lock of one lock file.
type heldKey struct{ path string }

// WriteLockPath is the database write lock file of the lock directory locksDir.
func WriteLockPath(locksDir string) string { return filepath.Join(locksDir, writeLockName) }

// WriteLockPath is this store's database write lock file.
func (s *Store) WriteLockPath() string { return WriteLockPath(filepath.Join(s.Dir, "locks")) }

// WithWriteLock runs fn, a database write transaction, holding the store's cross-process
// write lock. See the package comment above for the lock order and the re-entrancy rule.
func (s *Store) WithWriteLock(ctx context.Context, fn func(ctx context.Context) error) error {
	return WithWriteLockAt(ctx, s.WriteLockPath(), fn)
}

// WithWriteLockAt is WithWriteLock for a caller that holds only the lock file's path (the
// publisher and the indexer). The wait is the shorter of DefaultWriteLockWait and
// writeLockDeadlineShare of the time left before ctx's deadline. fn receives a context that
// marks the lock as held; every database call it makes must use that context.
func WithWriteLockAt(ctx context.Context, path string, fn func(ctx context.Context) error) error {
	if ctx.Value(heldKey{path}) != nil {
		return ErrWriteLockReentered
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	wait := DefaultWriteLockWait
	if dl, ok := ctx.Deadline(); ok {
		if share := time.Duration(float64(time.Until(dl)) * writeLockDeadlineShare); share < wait {
			wait = share
		}
	}
	if wait < 0 {
		wait = 0
	}
	unlock, err := Lock(path, wait)
	if errors.Is(err, ErrLockTimeout) {
		return ErrWriteLockTimeout
	}
	if err != nil {
		return err
	}
	defer unlock()
	return fn(context.WithValue(ctx, heldKey{path}, true))
}
