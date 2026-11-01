// Package confine gives every Wikory reader and writer an authorised root and refuses
// anything that would leave it: escaping names, symlinks at any depth, and non-regular
// files (spec 5.5 rule 7, 10.3).
package confine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrEscape     = errors.New("confine: path leaves its root")
	ErrSymlink    = errors.New("confine: symlink or reparse point refused")
	ErrNotRegular = errors.New("confine: not a regular file")
	ErrNotDir     = errors.New("confine: not a directory")
	ErrTooLarge   = errors.New("confine: file exceeds the size limit")
	ErrExist      = errors.New("confine: file already exists")
	ErrChanged    = errors.New("confine: target changed, nothing replaced")
	ErrHardlink   = errors.New("confine: file has more than one hard link, refused")
)

// TempPrefix names the temporary files atomic writes create beside their targets.
const TempPrefix = ".wikory-tmp-"

// BackupPrefix names the private backup a checked replace moves the live target to.
const BackupPrefix = ".wikory-backup-"

// Test hooks. Nil in production.
var (
	testHookBeforeRootOpen      func()
	testHookBeforeComponentOpen func(name string)
	testHookBeforeFinalOpen     func()
	testHookBeforeMoveAside     func()
	testHookAfterMoveAside      func()
	testHookAfterCompare        func()
	stepHook                    func(step string) // "moved", "compared", "installed"; crash tests only
)

// Split validates a root-relative slash path and returns its components. It refuses
// absolute paths, drive letters, backslashes, NUL bytes, and empty, "." or ".." components.
func Split(rel string) ([]string, error) {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.Contains(rel, `\`) ||
		strings.HasPrefix(rel, "/") || (len(rel) >= 2 && rel[1] == ':') {
		return nil, ErrEscape
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, ErrEscape
		}
		if err := checkPlatformName(p); err != nil {
			return nil, err
		}
	}
	return parts, nil
}

func randomSuffix() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("confine: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func tempName(target string) string   { return TempPrefix + target + "-" + randomSuffix() }
func backupName(target string) string { return BackupPrefix + target + "-" + randomSuffix() }

// BackupTarget returns the target name a backup entry belongs to, or false if entry is not
// a backup made by this package.
func BackupTarget(entry string) (string, bool) {
	const suffix = 17 // "-" and 16 hex digits
	if !strings.HasPrefix(entry, BackupPrefix) || len(entry) <= len(BackupPrefix)+suffix || entry[len(entry)-suffix] != '-' {
		return "", false
	}
	return entry[len(BackupPrefix) : len(entry)-suffix], true
}
