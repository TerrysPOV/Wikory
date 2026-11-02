//go:build unix

package store

import (
	"fmt"
	"os"
	"syscall"
)

// RestrictUmask makes every file this process creates owner-only, including the SQLite
// -wal and -shm files that SQLite creates itself. Files written into a repository get an
// explicit mode from confine, so they are unaffected.
func RestrictUmask() { syscall.Umask(0o077) }

// EnsurePrivateDir creates path (and parents) as 0700, refuses a symlink or a directory
// owned by someone else, and tightens a wider mode.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("store: %s is not a plain directory", path)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("store: %s is owned by another user", path)
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(path, 0o700)
	}
	return nil
}
