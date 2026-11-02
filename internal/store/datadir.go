// Package store owns the private, per-machine store (spec 4.3): its location, owner-only
// permissions, key, registry, database, lock, checkout registrations and the trust record.
package store

import (
	"fmt"
	"path/filepath"
)

// DataDir returns the local, non-roaming user-data directory for Wikory.
func DataDir(getenv func(string) string) (string, error) {
	if d := getenv("WIKORY_DATA_DIR"); d != "" {
		if !filepath.IsAbs(d) {
			return "", fmt.Errorf("store: WIKORY_DATA_DIR must be an absolute path, got %q", d)
		}
		return filepath.Clean(d), nil
	}
	return platformDataDir(getenv)
}
