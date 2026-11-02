//go:build windows

package app

import (
	"errors"
	"os"
)

// openHookLog opens the error log for appending. Windows has no FIFOs in the file namespace (a
// named pipe cannot be created at a plain path), but os.OpenFile follows symbolic links and
// junctions, and the regular-file check after the open does not stop that: a link to a regular
// file passes it. The log holds only the time and the kind of a failure, so following a link
// can misplace those lines but never discloses anything; doctor reports a log that is a link.
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

// openHookLogRead opens the log for reading, with the same limits as openHookLog: links are
// followed, so callers Lstat the path first and refuse anything that is not a regular file.
func openHookLogRead(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("hook log is not a regular file")
	}
	return f, nil
}
