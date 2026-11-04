package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrTooLarge means an object is bigger than the caller's read limit.
var ErrTooLarge = errors.New("gitx: object is larger than the read limit")

// TreeEntry is one regular file in a tree.
type TreeEntry struct {
	Path string
	OID  string
	Size int64
}

func okRev(rev string) bool { return rev == "HEAD" || shaRe.MatchString(rev) }

// ListTree lists the regular-file blobs (modes 100644 and 100755) of rev, recursively. Links
// and submodule entries are left out: a bootstrap reads content, and neither has any.
// rev is HEAD or a hex commit id, so no revision can pose as an option.
func (r *Repo) ListTree(ctx context.Context, rev string) ([]TreeEntry, error) {
	if !okRev(rev) {
		return nil, fmt.Errorf("gitx: refusing revision %q", rev)
	}
	out, err := r.git(ctx, nil, "ls-tree", "-r", "-z", "-l", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	var res []TreeEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(string(rec[:tab]))
		if len(meta) != 4 || meta[1] != "blob" || (meta[0] != "100644" && meta[0] != "100755") {
			continue
		}
		size, err := strconv.ParseInt(meta[3], 10, 64)
		if err != nil {
			continue
		}
		res = append(res, TreeEntry{Path: string(rec[tab+1:]), OID: meta[2], Size: size})
	}
	return res, nil
}

// ReadBlob returns the raw content of a blob given its full hex id, or ErrTooLarge when it is
// bigger than max. No filter, textconv or smudge runs: cat-file blob reads the stored bytes.
func (r *Repo) ReadBlob(ctx context.Context, oid string, max int64) ([]byte, error) {
	if !shaRe.MatchString(oid) {
		return nil, fmt.Errorf("gitx: %q is not a hex object id", oid)
	}
	sz, err := r.output(ctx, nil, "cat-file", "-s", oid)
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseInt(sz, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("gitx: unreadable object size %q", sz)
	}
	if n > max {
		return nil, ErrTooLarge
	}
	return r.git(ctx, nil, "cat-file", "blob", oid)
}

// Churn counts, per path, the commits reachable from HEAD since the given time that touched it.
// Renames are not followed, so a rename counts as a delete and an add.
func (r *Repo) Churn(ctx context.Context, since time.Time) (map[string]int, error) {
	counts := map[string]int{}
	head, err := r.HeadCommit(ctx)
	if err != nil || head == "" {
		return counts, err
	}
	out, err := r.git(ctx, nil, "log", "--no-renames", "--name-only", "-z", "--pretty=format:",
		"--since="+since.UTC().Format(time.RFC3339), head)
	if err != nil {
		return nil, err
	}
	for _, tok := range bytes.Split(out, []byte{0}) {
		p := strings.TrimLeft(string(tok), "\n")
		if p != "" {
			counts[p]++
		}
	}
	return counts, nil
}

// DefaultBranch is the branch bootstrap baselines: origin's HEAD when it is known, else main,
// else master, else "".
func (r *Repo) DefaultBranch(ctx context.Context) string {
	if out, err := r.output(ctx, nil, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && out != "" {
		return strings.TrimPrefix(out, "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if _, err := r.output(ctx, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	return ""
}

// LastChanged maps each path to the commit time of the newest commit reachable from HEAD that
// touched it. The log is read once, newest first, so the first sighting of a path is its last
// change. Paths deleted since appear too; callers look up only the paths in the tree.
func (r *Repo) LastChanged(ctx context.Context) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	tip, err := r.HeadCommit(ctx)
	if err != nil || tip == "" {
		return out, err
	}
	raw, err := r.git(ctx, nil, "log", "--no-renames", "--name-only", "-z", "--pretty=format:%x01%ct", tip)
	if err != nil {
		return nil, err
	}
	var cur time.Time
	for _, tok := range bytes.Split(raw, []byte{0}) {
		s := string(tok)
		if strings.HasPrefix(s, "\x01") {
			stamp, rest, _ := strings.Cut(s[1:], "\n")
			secs, err := strconv.ParseInt(stamp, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("gitx: unreadable commit time %q", stamp)
			}
			cur, s = time.Unix(secs, 0).UTC(), rest
		}
		s = strings.TrimLeft(s, "\n")
		if s == "" {
			continue
		}
		if _, seen := out[s]; !seen {
			out[s] = cur
		}
	}
	return out, nil
}

// Branch is a local branch that holds commits HEAD does not.
type Branch struct {
	Name  string
	Ahead int       // commits on the branch that HEAD lacks
	Tip   time.Time // commit time of the branch tip
}

// Branches lists the local branches with commits HEAD lacks, in name order. A bootstrap
// baselines HEAD, so these are the branches whose work it will not see. Full ref names are
// used throughout, so no branch name can pose as an option.
func (r *Repo) Branches(ctx context.Context) ([]Branch, error) {
	out, err := r.git(ctx, nil, "for-each-ref", "--format=%(refname)%09%(committerdate:unix)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	var res []Branch
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		ref, stamp, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(ref, "refs/heads/") {
			continue
		}
		n, err := r.output(ctx, nil, "rev-list", "--count", "HEAD.."+ref)
		if err != nil {
			return nil, err
		}
		ahead, err := strconv.Atoi(n)
		if err != nil {
			return nil, fmt.Errorf("gitx: unreadable commit count %q", n)
		}
		if ahead == 0 {
			continue
		}
		secs, _ := strconv.ParseInt(stamp, 10, 64)
		res = append(res, Branch{Name: strings.TrimPrefix(ref, "refs/heads/"), Ahead: ahead, Tip: time.Unix(secs, 0).UTC()})
	}
	return res, nil
}

// Worktree is another working tree of the same repository.
type Worktree struct {
	Path   string
	Branch string // empty when the tree has a detached HEAD
	Head   string
}

// Worktrees lists the working trees of this repository other than the one r operates in. A
// worktree whose directory is gone (git calls it prunable) is left out: it holds nothing to read.
func (r *Repo) Worktrees(ctx context.Context) ([]Worktree, error) {
	out, err := r.git(ctx, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	self, _ := filepath.EvalSymlinks(r.Top)
	var res []Worktree
	for _, block := range strings.Split(string(out), "\n\n") {
		var w Worktree
		skip := false
		for _, line := range strings.Split(block, "\n") {
			key, val, _ := strings.Cut(line, " ")
			switch key {
			case "worktree":
				w.Path = val
			case "HEAD":
				w.Head = val
			case "branch":
				w.Branch = strings.TrimPrefix(val, "refs/heads/")
			case "bare", "prunable":
				skip = true
			}
		}
		if w.Path == "" || skip {
			continue
		}
		if p, err := filepath.EvalSymlinks(w.Path); err == nil && p == self {
			continue
		}
		res = append(res, w)
	}
	return res, nil
}
