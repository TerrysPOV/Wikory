//go:build windows

package store

import (
	"errors"
	"path/filepath"
)

// LOCALAPPDATA is local and non-roaming, unlike APPDATA.
func platformDataDir(getenv func(string) string) (string, error) {
	d := getenv("LOCALAPPDATA")
	if d == "" {
		return "", errors.New("store: LOCALAPPDATA is not set")
	}
	return filepath.Join(d, "wikory"), nil
}
