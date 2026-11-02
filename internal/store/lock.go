package store

import "errors"

// ErrLockTimeout means another process held the lock for the whole wait.
var ErrLockTimeout = errors.New("store: lock not acquired in time")
