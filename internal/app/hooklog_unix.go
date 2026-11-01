//go:build unix

package app

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// openHookLog opens the error log for appending without ever blocking: O_NONBLOCK means a
// FIFO at that path cannot stall the open (or a later write), O_NOFOLLOW refuses a symlink,
// and the fstat check refuses anything but a regular file.
func openHookLog(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, errors.New("hook log is not a regular file")
	}
	return os.NewFile(uintptr(fd), path), nil
}
