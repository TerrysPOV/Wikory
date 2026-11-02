//go:build windows

package store

import (
	"fmt"
	"os"
)

// RestrictUmask is a no-op on Windows: owner-only ACLs are increment 1d (spec 12).
func RestrictUmask() {}

// EnsurePrivateDir creates path and refuses a symlink or reparse point. Until 1d it relies
// on the inherited ACL of %LOCALAPPDATA%, which doctor reports.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !fi.IsDir() {
		return fmt.Errorf("store: %s is not a plain directory", path)
	}
	return nil
}
