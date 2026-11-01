//go:build darwin

package store

import (
	"errors"
	"path/filepath"
)

func platformDataDir(getenv func(string) string) (string, error) {
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("store: HOME is not set")
	}
	return filepath.Join(home, "Library", "Application Support", "wikory"), nil
}
