//go:build unix

package confine

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// dirOpenFlags opens a directory component: O_NOFOLLOW makes the kernel refuse a symlink
// at that name (ELOOP on Linux, ENOTDIR on macOS when combined with O_DIRECTORY).
const dirOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// rootOpenFlags opens the root's final component, no-follow.
const rootOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// fileOpenFlags: O_NOFOLLOW refuses a symlinked final name; O_NONBLOCK keeps a FIFO from
// blocking the open, and the post-open fstat then refuses it as non-regular.
const fileOpenFlags = unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC

func checkPlatformName(string) error { return nil }

// dirH is an open directory file descriptor.
type dirH struct{ fd int }

func openat(dirfd int, name string, flags int, perm uint32) (int, error) {
	for {
		fd, err := unix.Openat(dirfd, name, flags, perm)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func isLink(dirfd int, name string) (link, dir, ok bool) {
	var st unix.Stat_t
	if unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return false, false, false
	}
	return st.Mode&unix.S_IFMT == unix.S_IFLNK, st.Mode&unix.S_IFMT == unix.S_IFDIR, true
}

// refusal maps an open error: ELOOP or ENOTDIR at a name that is a symlink is ErrSymlink
// (both errnos occur, by platform); ENOTDIR at a plain non-directory is ErrNotDir.
func refusal(dirfd int, name, op string, err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.EMLINK) {
		link, _, ok := isLink(dirfd, name)
		switch {
		case ok && link, errors.Is(err, unix.ELOOP):
			return ErrSymlink
		case errors.Is(err, unix.ENOTDIR):
			return ErrNotDir
		}
	}
	return &fs.PathError{Op: op, Path: name, Err: err}
}

// openRootDir opens path's final component no-follow. The Lstat identity check is a
// supplement: it also refuses a root replaced by another plain directory mid-open.
func openRootDir(path string) (*dirH, error) {
	var before unix.Stat_t
	if err := unix.Lstat(path, &before); err != nil {
		return nil, &fs.PathError{Op: "lstat", Path: path, Err: err}
	}
	if before.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, ErrSymlink
	}
	if testHookBeforeRootOpen != nil {
		testHookBeforeRootOpen()
	}
	fd, err := openat(unix.AT_FDCWD, path, rootOpenFlags, 0)
	if err != nil {
		return nil, refusal(unix.AT_FDCWD, path, "open", err)
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil || after.Dev != before.Dev || after.Ino != before.Ino {
		unix.Close(fd)
		return nil, ErrSymlink
	}
	return &dirH{fd: fd}, nil
}

// reopen opens a new, independent description of the same directory (its own offset).
func (d *dirH) reopen() (*dirH, error) {
	fd, err := openat(d.fd, ".", dirOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	return &dirH{fd: fd}, nil
}

func (d *dirH) openDir(name string) (*dirH, error) {
	fd, err := openat(d.fd, name, dirOpenFlags, 0)
	if err != nil {
		return nil, refusal(d.fd, name, "openat", err)
	}
	return &dirH{fd: fd}, nil
}

func (d *dirH) openFile(name string, flags int, perm fs.FileMode) (*os.File, error) {
	fd, err := openat(d.fd, name, flags|fileOpenFlags, uint32(perm.Perm()))
	if err != nil {
		return nil, refusal(d.fd, name, "openat", err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, ErrNotRegular
	}
	// A second link to the inode may lead outside the root (an archive that preserves hard
	// links; macOS has no protected_hardlinks). Reading it leaks, and appending or writing
	// through it would modify the outside file, so any existing file with Nlink > 1 is refused.
	// A file created by this call has one link and is unaffected.
	if st.Nlink > 1 {
		unix.Close(fd)
		return nil, ErrHardlink
	}
	if err := unix.SetNonblock(fd, false); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (d *dirH) mkdir(name string, perm fs.FileMode) error {
	if err := unix.Mkdirat(d.fd, name, uint32(perm)); err != nil {
		return &fs.PathError{Op: "mkdirat", Path: name, Err: err}
	}
	sub, err := d.openDir(name)
	if err != nil {
		return err
	}
	defer sub.close()
	return unix.Fchmod(sub.fd, uint32(perm)) // exact mode whatever the umask
}

func (d *dirH) link(oldname, newname string) error {
	if err := unix.Linkat(d.fd, oldname, d.fd, newname, 0); err != nil {
		return &os.LinkError{Op: "linkat", Old: oldname, New: newname, Err: err}
	}
	return nil
}

func (d *dirH) rename(oldname, newname string) error {
	if err := unix.Renameat(d.fd, oldname, d.fd, newname); err != nil {
		return &os.LinkError{Op: "renameat", Old: oldname, New: newname, Err: err}
	}
	return nil
}

func (d *dirH) remove(name string) error {
	err := unix.Unlinkat(d.fd, name, 0)
	if errors.Is(err, unix.EISDIR) || errors.Is(err, unix.EPERM) {
		err = unix.Unlinkat(d.fd, name, unix.AT_REMOVEDIR)
	}
	if err != nil {
		return &fs.PathError{Op: "unlinkat", Path: name, Err: err}
	}
	return nil
}

func (d *dirH) readDir() ([]fs.DirEntry, error) {
	n, err := d.reopen()
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(n.fd), ".")
	defer f.Close()
	return f.ReadDir(-1)
}

// sync makes renames and links in the directory durable.
func (d *dirH) sync() error { return unix.Fsync(d.fd) }

func (d *dirH) close() error { return unix.Close(d.fd) }
