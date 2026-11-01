package confine

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Root is an authorised directory, held open as a directory handle.
//
// Threat model. Root paths come from Wikory itself (the git top level, the private store,
// an approved transcript root). Open resolves the root's parent once with EvalSymlinks, a
// documented trusted step (macOS /tmp is a symlink to /private/tmp), and then opens the
// final component with no-follow semantics, so a root whose final component is, or is
// swapped to, a symlink is refused. Below the root nothing is ever resolved from a path
// string: each component is opened relative to the handle of its parent with no-follow
// semantics (unix: openat with O_NOFOLLOW, plus O_DIRECTORY for directories), so a symlink
// at any depth, including one swapped in after any earlier check, is refused by the kernel
// rather than followed. Identity comparisons supplement this; they never substitute for it.
// Windows uses os.Root with reparse-point checks before and after each open; its residual
// race is stated in dir_windows.go.
type Root struct {
	dir string // resolved path, for display and relative-path computation only; never opened
	h   *dirH
}

// Open opens dir once, refusing a symlinked final component.
func Open(dir string) (*Root, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("confine: root %q is not absolute", dir)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir)) // the one trusted resolution
	if err != nil {
		return nil, err
	}
	resolved := filepath.Join(parent, filepath.Base(dir))
	h, err := openRootDir(resolved)
	if err != nil {
		return nil, err
	}
	return &Root{dir: resolved, h: h}, nil
}

// Path is the resolved root directory, for display and relative paths only.
func (r *Root) Path() string { return r.dir }

// Close releases the root.
func (r *Root) Close() error { return r.h.close() }

// walk opens each directory component in turn, no-follow, and returns a new handle on the
// last one; with no components it returns a new handle on the root. The caller closes it.
func (r *Root) walk(dirs []string) (*dirH, error) {
	cur, err := r.h.reopen()
	if err != nil {
		return nil, err
	}
	for _, c := range dirs {
		if testHookBeforeComponentOpen != nil {
			testHookBeforeComponentOpen(c)
		}
		next, err := cur.openDir(c)
		cur.close()
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

func (r *Root) parent(rel string) (*dirH, string, error) {
	parts, err := Split(rel)
	if err != nil {
		return nil, "", err
	}
	dir, err := r.walk(parts[:len(parts)-1])
	if err != nil {
		return nil, "", err
	}
	return dir, parts[len(parts)-1], nil
}

func (r *Root) openFile(rel string, flags int, perm fs.FileMode) (*os.File, error) {
	dir, name, err := r.parent(rel)
	if err != nil {
		return nil, err
	}
	defer dir.close()
	if testHookBeforeFinalOpen != nil {
		testHookBeforeFinalOpen()
	}
	return dir.openFile(name, flags, perm)
}

// OpenRead opens a regular file for reading.
func (r *Root) OpenRead(rel string) (*os.File, error) { return r.openFile(rel, os.O_RDONLY, 0) }

// ReadFile reads a regular file of at most max bytes.
func (r *Root) ReadFile(rel string, max int64) ([]byte, error) {
	f, err := r.OpenRead(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readLimited(f, max)
}

func readLimited(f *os.File, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, ErrTooLarge
	}
	return data, nil
}

// writeTemp creates, fills, chmods and fsyncs a temp file beside name.
func writeTemp(dir *dirH, name string, data []byte, perm fs.FileMode) (string, error) {
	tmp := tempName(name)
	f, err := dir.openFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm.Perm())
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Chmod(perm.Perm()) // exact mode whatever the umask
	}
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		dir.remove(tmp)
		return "", werr
	}
	return tmp, nil
}

// WriteFileAtomic replaces rel with data: temp file, fsync, rename, directory fsync. It is
// for files only Wikory writes (the private store); a file a human may edit goes through
// ReplaceIfUnchanged.
func (r *Root) WriteFileAtomic(rel string, data []byte, perm fs.FileMode) error {
	dir, name, err := r.parent(rel)
	if err != nil {
		return err
	}
	defer dir.close()
	if cur, err := dir.openFile(name, os.O_RDONLY, 0); err == nil {
		cur.Close()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err // a symlink or a non-regular file at the target is refused, never replaced
	}
	tmp, err := writeTemp(dir, name, data, perm)
	if err != nil {
		return err
	}
	if err := dir.rename(tmp, name); err != nil {
		dir.remove(tmp)
		return err
	}
	return dir.sync()
}

// ReplaceIfUnchanged replaces rel with data only if its current content satisfies want,
// without ever overwriting a human edit (spec 4.4). Under the caller's publish lock:
//
//  1. the staged content is written to a complete temp file;
//  2. the live target is renamed to a private backup name, so no later write by a path
//     reaches the content being judged;
//  3. the backup is read (no-follow, regular only) and judged; if want refuses it, the
//     backup is put back with a no-clobber hard link and ErrChanged is returned;
//  4. the staged file is installed with a no-clobber hard link; if the name exists again,
//     a human created it after step 2: the human file is kept, the backup is kept beside it,
//     and ErrChanged is returned;
//  5. the temp and backup names are removed and the directory is fsynced.
//
// Residual: an editor that already holds the original file open and writes through that
// descriptor after step 3 writes to the moved inode, which is then unlinked; that edit is
// lost. Recovery restores a backup whose target is missing (RestoreBackups).
func (r *Root) ReplaceIfUnchanged(rel string, data []byte, perm fs.FileMode, want func(current []byte) bool) error {
	dir, name, err := r.parent(rel)
	if err != nil {
		return err
	}
	defer dir.close()
	tmp, err := writeTemp(dir, name, data, perm)
	if err != nil {
		return err
	}
	defer dir.remove(tmp)
	if testHookBeforeMoveAside != nil {
		testHookBeforeMoveAside()
	}
	bak := backupName(name)
	if err := dir.rename(name, bak); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s is missing", ErrChanged, rel)
		}
		return err
	}
	step("moved")
	if testHookAfterMoveAside != nil {
		testHookAfterMoveAside()
	}
	cur, rerr := dir.openFile(bak, os.O_RDONLY, 0)
	var current []byte
	if rerr == nil {
		current, rerr = readLimited(cur, 1<<26)
		cur.Close()
	}
	if rerr != nil || !want(current) {
		if lerr := dir.link(bak, name); lerr == nil {
			dir.remove(bak)
		}
		dir.sync()
		if errors.Is(rerr, ErrHardlink) {
			return fmt.Errorf("%w: %w: %s", ErrChanged, ErrHardlink, rel)
		}
		return fmt.Errorf("%w: %s", ErrChanged, rel)
	}
	step("compared")
	if testHookAfterCompare != nil {
		testHookAfterCompare()
	}
	if err := dir.link(tmp, name); err != nil {
		dir.sync()
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s was recreated during the replace; backup kept as %s", ErrChanged, rel, bak)
		}
		return err
	}
	step("installed")
	dir.remove(bak)
	return dir.sync()
}

// RestoreBackups puts back every backup in the directory of rel whose target name is
// missing, and removes backups whose target content satisfies done. It returns the names
// of backups it left in place. Recovery calls it under the publish lock.
func (r *Root) RestoreBackups(rel string, done func(target string, current []byte) bool) ([]string, error) {
	parts, err := Split(rel)
	if err != nil {
		return nil, err
	}
	dir, err := r.walk(parts)
	if err != nil {
		return nil, err
	}
	defer dir.close()
	ents, err := dir.readDir()
	if err != nil {
		return nil, err
	}
	var kept []string
	for _, e := range ents {
		target, ok := BackupTarget(e.Name())
		if !ok {
			continue
		}
		if lerr := dir.link(e.Name(), target); lerr == nil {
			dir.remove(e.Name())
			continue
		}
		f, err := dir.openFile(target, os.O_RDONLY, 0)
		if err == nil {
			cur, rerr := readLimited(f, 1<<26)
			f.Close()
			if rerr == nil && done(target, cur) {
				dir.remove(e.Name())
				continue
			}
		}
		kept = append(kept, e.Name())
	}
	return kept, dir.sync()
}

// CreateExclusive writes rel only if it does not exist, via a hard link from a complete
// temp file, so an existing file is never replaced (no-clobber).
func (r *Root) CreateExclusive(rel string, data []byte, perm fs.FileMode) error {
	dir, name, err := r.parent(rel)
	if err != nil {
		return err
	}
	defer dir.close()
	tmp, err := writeTemp(dir, name, data, perm)
	if err != nil {
		return err
	}
	lerr := dir.link(tmp, name)
	dir.remove(tmp)
	if lerr != nil {
		if errors.Is(lerr, fs.ErrExist) {
			return ErrExist
		}
		return lerr
	}
	return dir.sync()
}

// AppendFile appends data to a regular file, creating it if needed, and fsyncs it.
func (r *Root) AppendFile(rel string, data []byte, perm fs.FileMode) error {
	f, err := r.openFile(rel, os.O_WRONLY|os.O_APPEND|os.O_CREATE, perm)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// MkdirAll creates each missing directory component with exactly perm.
func (r *Root) MkdirAll(rel string, perm fs.FileMode) error {
	parts, err := Split(rel)
	if err != nil {
		return err
	}
	cur, err := r.h.reopen()
	if err != nil {
		return err
	}
	for _, c := range parts {
		if err := cur.mkdir(c, perm.Perm()); err != nil && !errors.Is(err, fs.ErrExist) {
			cur.close()
			return err
		}
		next, err := cur.openDir(c)
		cur.close()
		if err != nil {
			return err
		}
		cur = next
	}
	return cur.close()
}

// ReadDir lists a directory under the root. "." is the root itself.
func (r *Root) ReadDir(rel string) ([]fs.DirEntry, error) {
	var parts []string
	if rel != "." {
		p, err := Split(rel)
		if err != nil {
			return nil, err
		}
		parts = p
	}
	dir, err := r.walk(parts)
	if err != nil {
		return nil, err
	}
	defer dir.close()
	return dir.readDir()
}

// Remove unlinks a file (a symlink itself is removed, never followed).
func (r *Root) Remove(rel string) error {
	dir, name, err := r.parent(rel)
	if err != nil {
		return err
	}
	defer dir.close()
	return dir.remove(name)
}
