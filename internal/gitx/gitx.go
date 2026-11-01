// Package gitx reads git facts by running the git CLI with defences against a hostile
// repository configuration.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrNotRepo       = errors.New("gitx: not inside a git work tree")
	ErrAmbiguous     = errors.New("gitx: short object id is ambiguous")
	ErrUnknownObject = errors.New("gitx: object not found")
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// Repo is one worktree of one clone.
type Repo struct {
	Top       string
	CommonDir string
	GitDir    string
	// neutralise overrides every filter and diff driver the repository's own config names,
	// so no clean, smudge, process, textconv or external diff command it defines can run.
	neutralise []string
}

// safety applies to every invocation, whatever the repository's configuration says. Wikory
// never widens safe.directory. Comparing like with like through the repository's clean
// filters is deferred to the engine specification (spec 14.6): blobs are hashed raw.
var safety = []string{"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.hooksPath=" + os.DevNull,
	"-c", "core.attributesFile=" + os.DevNull, "-c", "diff.external=", "--no-optional-locks", "--literal-pathspecs"}

var driverRe = regexp.MustCompile(`^(filter|diff)\.(.+)\.(clean|smudge|process|required|textconv|command)$`)

// Error carries git's stderr.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.Args, " "), e.Err, e.Stderr)
}
func (e *Error) Unwrap() error { return e.Err }

func run(ctx context.Context, dir string, extra []string, stdin []byte, args ...string) ([]byte, error) {
	full := append(append(append([]string{"-C", dir}, safety...), extra...), args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), &Error{Args: args, Stderr: strings.TrimSpace(errb.String()), Err: err}
	}
	return out.Bytes(), nil
}

func cleanPath(p string) (string, error) {
	return filepath.EvalSymlinks(filepath.FromSlash(strings.TrimSpace(p)))
}

func (r *Repo) git(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	return run(ctx, r.Top, r.neutralise, stdin, args...)
}

// Discover finds the worktree containing dir and prepares the driver overrides.
func Discover(ctx context.Context, dir string) (*Repo, error) {
	out, err := run(ctx, dir, nil, nil, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir", "--absolute-git-dir")
	if err != nil {
		return nil, ErrNotRepo
	}
	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(lines) != 3 || strings.TrimSpace(lines[0]) == "" {
		return nil, ErrNotRepo
	}
	var r Repo
	for i, dst := range []*string{&r.Top, &r.CommonDir, &r.GitDir} {
		if *dst, err = cleanPath(lines[i]); err != nil {
			return nil, err
		}
	}
	// Reading config runs nothing. Every named filter and diff driver is overridden to empty.
	names, _ := run(ctx, r.Top, nil, nil, "config", "--null", "--name-only", "--get-regexp", `^(filter|diff)\.`)
	seen := map[string]bool{}
	for _, n := range bytes.Split(names, []byte{0}) {
		m := driverRe.FindStringSubmatch(strings.TrimSpace(string(n)))
		if m == nil {
			continue
		}
		for _, key := range []string{m[1] + "." + m[2] + ".clean", m[1] + "." + m[2] + ".smudge", m[1] + "." + m[2] + ".process",
			m[1] + "." + m[2] + ".textconv", m[1] + "." + m[2] + ".command"} {
			if !seen[key] {
				seen[key] = true
				r.neutralise = append(r.neutralise, "-c", key+"=")
			}
		}
		if req := m[1] + "." + m[2] + ".required"; !seen[req] {
			seen[req] = true
			r.neutralise = append(r.neutralise, "-c", req+"=false")
		}
	}
	return &r, nil
}

// ObjectFormat is the repository's hash algorithm: "sha1" or "sha256".
func (r *Repo) ObjectFormat(ctx context.Context) (string, error) {
	out, err := r.git(ctx, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ResolveObject turns a full or short object id into the unique full id of an object of
// kind ("blob" or "commit"). An ambiguous short id is an error, never a guess.
func (r *Repo) ResolveObject(ctx context.Context, id, kind string) (string, error) {
	if !shaRe.MatchString(id) || (kind != "blob" && kind != "commit") {
		return "", fmt.Errorf("gitx: %q is not an object id", id)
	}
	out, err := r.git(ctx, nil, "rev-parse", "--verify", "--end-of-options", id+"^{"+kind+"}")
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && strings.Contains(ge.Stderr, "ambiguous") {
			return "", fmt.Errorf("%w: %s", ErrAmbiguous, id)
		}
		return "", fmt.Errorf("%w: %s", ErrUnknownObject, id)
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadCommit is the full HEAD commit id, or "" before the first commit.
func (r *Repo) HeadCommit(ctx context.Context) (string, error) {
	out, err := r.git(ctx, nil, "rev-parse", "-q", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadTree is the HEAD tree id, or "" before the first commit.
func (r *Repo) HeadTree(ctx context.Context) (string, error) {
	out, err := r.git(ctx, nil, "rev-parse", "-q", "--verify", "HEAD^{tree}")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadBlobs maps each path that is a blob at HEAD to its blob id.
func (r *Repo) HeadBlobs(ctx context.Context, paths []string) (map[string]string, error) {
	res := map[string]string{}
	if len(paths) == 0 {
		return res, nil
	}
	if head, _ := r.HeadCommit(ctx); head == "" {
		return res, nil
	}
	out, err := r.git(ctx, nil, append([]string{"ls-tree", "-z", "--full-tree", "HEAD", "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	for _, rec := range bytes.Split(out, []byte{0}) {
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			continue
		}
		meta := strings.Fields(string(rec[:tab]))
		if len(meta) == 3 && meta[1] == "blob" {
			res[string(rec[tab+1:])] = meta[2]
		}
	}
	return res, nil
}

// HashBlob is the blob id of content, hashed raw with no repository filter run.
func (r *Repo) HashBlob(ctx context.Context, path string, content []byte) (string, error) {
	_ = path // the path no longer selects filters: blobs are hashed raw (spec 14.6 defers clean-filter comparison)
	out, err := r.git(ctx, content, "hash-object", "--stdin", "--no-filters")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CommitExists reports whether sha names a commit. Option-shaped input is refused.
func (r *Repo) CommitExists(ctx context.Context, sha string) (bool, error) {
	if !shaRe.MatchString(sha) || len(sha) < 7 {
		return false, fmt.Errorf("gitx: %q is not a commit id", sha)
	}
	_, err := r.git(ctx, nil, "cat-file", "-e", sha+"^{commit}")
	return err == nil, nil
}

// Branch is the short branch name, or "" when detached.
func (r *Repo) Branch(ctx context.Context) string {
	out, err := r.git(ctx, nil, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// OriginURL is remote.origin.url, else the first remote's URL, else "".
func (r *Repo) OriginURL(ctx context.Context) string {
	if out, err := r.git(ctx, nil, "config", "--get", "remote.origin.url"); err == nil {
		return strings.TrimSpace(string(out))
	}
	out, err := r.git(ctx, nil, "remote")
	if err != nil {
		return ""
	}
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if first == "" {
		return ""
	}
	url, err := r.git(ctx, nil, "remote", "get-url", first) // first comes from git itself
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(url))
}

// Dirty lists changed and untracked paths under pathspec, repository-relative.
func (r *Repo) Dirty(ctx context.Context, pathspec string) ([]string, error) {
	out, err := r.git(ctx, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", pathspec)
	if err != nil {
		return nil, err
	}
	var paths []string
	recs := bytes.Split(out, []byte{0})
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		paths = append(paths, string(rec[3:]))
		if rec[0] == 'R' || rec[0] == 'C' {
			i++ // the next record is the original path of a rename or copy
		}
	}
	return paths, nil
}
