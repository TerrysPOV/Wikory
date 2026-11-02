//go:build windows

package confine

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// checkPlatformName refuses device names, alternate data streams and names Windows
// silently rewrites (a trailing dot or space).
func checkPlatformName(c string) error {
	if windowsUnsafeName(c) {
		return ErrEscape
	}
	return nil
}

// dirH on Windows is an os.Root: each component is opened relative to its parent's handle,
// and the reparse attribute of the name is inspected before and after the open, refusing
// symlinks and junctions. os.Root refuses absolute links (every junction is absolute) on
// its own.
//
// Residual (pending-ci, beta): os.Root follows a relative in-root symlink, so a swap from a
// plain directory to such a symlink and back again between the before and after checks is
// not detected; creating that symlink needs a privilege (Developer Mode or
// SeCreateSymbolicLinkPrivilege). A true no-follow open needs NtCreateFile with
// FILE_OPEN_REPARSE_POINT relative to the parent handle; that is deferred to 1d.
type dirH struct{ r *os.Root }

func plain(fi fs.FileInfo) bool { return fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) == 0 }

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrExist):
		return err
	case strings.Contains(err.Error(), "path escapes"):
		return ErrSymlink
	}
	return err
}

func openRootDir(path string) (*dirH, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !plain(before) || !before.IsDir() {
		return nil, ErrSymlink
	}
	if testHookBeforeRootOpen != nil {
		testHookBeforeRootOpen()
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	after, err := r.Stat(".")
	now, lerr := os.Lstat(path)
	if err != nil || lerr != nil || !plain(now) || !os.SameFile(before, after) || !os.SameFile(now, after) {
		r.Close()
		return nil, ErrSymlink
	}
	return &dirH{r: r}, nil
}

func (d *dirH) reopen() (*dirH, error) {
	r, err := d.r.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	return &dirH{r: r}, nil
}

func (d *dirH) openDir(name string) (*dirH, error) {
	before, err := d.r.Lstat(name)
	if err != nil {
		return nil, mapErr(err)
	}
	if !plain(before) {
		return nil, ErrSymlink
	}
	if !before.IsDir() {
		return nil, ErrNotDir
	}
	sub, err := d.r.OpenRoot(name)
	if err != nil {
		return nil, mapErr(err)
	}
	after, err := sub.Stat(".")
	now, lerr := d.r.Lstat(name)
	if err != nil || lerr != nil || !plain(now) || !os.SameFile(before, after) || !os.SameFile(now, after) {
		sub.Close()
		return nil, ErrSymlink
	}
	return &dirH{r: sub}, nil
}

func (d *dirH) openFile(name string, flags int, perm fs.FileMode) (*os.File, error) {
	before, err := d.r.Lstat(name)
	exists := err == nil
	switch {
	case err != nil && !(errors.Is(err, fs.ErrNotExist) && flags&os.O_CREATE != 0):
		return nil, mapErr(err)
	case exists && !plain(before):
		return nil, ErrSymlink
	}
	f, err := d.r.OpenFile(name, flags, perm)
	if err != nil {
		return nil, mapErr(err)
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() {
		f.Close()
		return nil, ErrNotRegular
	}
	// A second link to the file may lead outside the root: refuse any file with more than one.
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		f.Close()
		return nil, err
	}
	if info.NumberOfLinks > 1 {
		f.Close()
		return nil, ErrHardlink
	}
	now, err := d.r.Lstat(name)
	if err != nil || !plain(now) || !os.SameFile(now, after) || (exists && !os.SameFile(before, after)) {
		f.Close()
		return nil, ErrSymlink
	}
	return f, nil
}

func (d *dirH) mkdir(name string, perm fs.FileMode) error {
	if err := d.r.Mkdir(name, perm); err != nil {
		return mapErr(err)
	}
	return d.r.Chmod(name, perm)
}

func (d *dirH) link(oldname, newname string) error { return mapErr(d.r.Link(oldname, newname)) }

func (d *dirH) rename(oldname, newname string) error { return mapErr(d.r.Rename(oldname, newname)) }

func (d *dirH) remove(name string) error { return mapErr(d.r.Remove(name)) }

func (d *dirH) readDir() ([]fs.DirEntry, error) {
	f, err := d.r.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// sync: Windows cannot flush a directory handle; NTFS journals the rename itself.
func (d *dirH) sync() error { return nil }

func (d *dirH) close() error { return d.r.Close() }
