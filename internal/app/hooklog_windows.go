//go:build windows

package app

import (
	"errors"
	"os"
)

// openHookLog: Windows has no FIFOs in the file namespace; a named pipe cannot be created
// at a plain path, so the regular-file check after the open is the guard.
func openHookLog(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("hook log is not a regular file")
	}
	return f, nil
}
