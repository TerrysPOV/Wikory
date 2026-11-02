//go:build unix

package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"
)

func TestStoreIsOwnerOnlyUnderPermissiveUmask(t *testing.T) {
	old := syscall.Umask(0o022) // the common default: group and other can read
	defer syscall.Umask(old)
	s := open(t, t.TempDir(), ident("wkp_P", "github.com/o/r", "/c/.git", "/c"), true)
	if err := s.Bump(context.Background(), "x"); err != nil { // makes SQLite create its -wal file
		t.Fatal(err)
	}
	seen := 0
	_ = filepath.WalkDir(s.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, _ := d.Info()
		seen++
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %v", p, fi.Mode().Perm())
		}
		return nil
	})
	if seen < 4 {
		t.Fatalf("walked only %d entries; the -wal file was expected", seen)
	}
}
