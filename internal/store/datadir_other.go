//go:build !darwin && !windows

package store

import (
	"errors"
	"path/filepath"
)

func platformDataDir(getenv func(string) string) (string, error) {
	if x := getenv("XDG_DATA_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "wikory"), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("store: HOME is not set")
	}
	return filepath.Join(home, ".local", "share", "wikory"), nil
}
