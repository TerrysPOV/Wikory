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
	"strconv"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/confine"
)

var (
	ErrNotRepo       = errors.New("gitx: not inside a git work tree")
	ErrAmbiguous     = errors.New("gitx: short object id is ambiguous")
	ErrUnknownObject = errors.New("gitx: object not found")
	// ErrWorktreeRedirected means git reports a work tree that is neither the current
	// directory nor one of its ancestors, as core.worktree in the repository's config can
	// make it. Wikory writes inside the work tree, so it refuses to follow the redirection.
	ErrWorktreeRedirected = errors.New("gitx: the repository config redirects its work tree outside the current directory")
	// ErrUnsafeExclude means the repository's info/exclude is not a regular file (a FIFO or a
	// device, or a link to one). git opens it on every status and would block on it for ever.
	ErrUnsafeExclude = errors.New("gitx: info/exclude is not a regular file; git status would block on it")
)

var shaRe = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

// Repo is one worktree of one clone.
type Repo struct {
	Top       string
	CommonDir string
	GitDir    string
	// Confirmed is true only when git itself vouches that Top is a worktree of this clone.
	Confirmed bool
	// neutralise holds key/value config overrides, one per filter and diff driver setting
	// the repository's own config names, so no clean, smudge, process, textconv or command
	// it defines can run. They reach git through GIT_CONFIG_KEY_n / GIT_CONFIG_VALUE_n, never
	// as `-c key=value`: a subsection name may contain '=', and git would split a `-c`
	// argument at the first one.
	neutralise [][2]string
}

// safety applies to every invocation, whatever the repository's configuration says. Wikory
// never widens safe.directory. Comparing like with like through the repository's clean
// filters is deferred to the engine specification (spec 14.6): blobs are hashed raw.
// log.showSignature=false keeps a repository's own log.showSignature from making every log
// verify signatures, which runs whatever gpg.program its config names.
var safety = []string{"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.hooksPath=" + os.DevNull,
	"-c", "core.attributesFile=" + os.DevNull, "-c", "core.excludesFile=", "-c", "diff.external=", "-c", "log.showSignature=false",
	"--no-optional-locks", "--literal-pathspecs"}

// baseConfig is applied to every invocation, after any environment config is cleared. It
// keeps git from walking into submodules, which have config and attributes of their own that
// no list built from this repository's config can cover.
var baseConfig = [][2]string{{"status.submoduleSummary", "false"}, {"submodule.recurse", "false"}, {"diff.ignoreSubmodules", "all"}}

// gitEnv is the environment for one git invocation. The caller's GIT_CONFIG_COUNT and its
// KEY and VALUE entries are dropped and replaced by baseConfig followed by extra, each key
// and value in its own variable. Lazy fetching (a partial clone reaching its promisor remote for
// a missing object) and replace objects (refs/replace standing one object in for another) are
// switched off.
func gitEnv(extra [][2]string) []string {
	all := append(append([][2]string{}, baseConfig...), extra...)
	env := make([]string, 0, len(os.Environ())+2*len(all)+5)
	for _, kv := range os.Environ() {
		up := strings.ToUpper(kv)
		if strings.HasPrefix(up, "GIT_CONFIG_COUNT=") || strings.HasPrefix(up, "GIT_CONFIG_KEY_") || strings.HasPrefix(up, "GIT_CONFIG_VALUE_") ||
			strings.HasPrefix(up, "GIT_OPTIONAL_LOCKS=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(all)))
	for i, o := range all {
		env = append(env, "GIT_CONFIG_KEY_"+strconv.Itoa(i)+"="+o[0], "GIT_CONFIG_VALUE_"+strconv.Itoa(i)+"="+o[1])
	}
	return append(env, "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1")
}

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

func run(ctx context.Context, dir string, extra [][2]string, stdin []byte, args ...string) ([]byte, error) {
	full := append(append([]string{"-C", dir}, safety...), args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = gitEnv(extra)
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

// output runs git and returns its standard output without surrounding whitespace.
func (r *Repo) output(ctx context.Context, stdin []byte, args ...string) (string, error) {
	out, err := r.git(ctx, stdin, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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
	// core.worktree in the repository's config can name any directory. Everything Wikory
	// writes goes inside the work tree, so one that does not contain dir is refused.
	if !containsDir(r.Top, dir) {
		return nil, ErrWorktreeRedirected
	}
	// Reading config runs nothing. Every named filter and diff driver is overridden to empty,
	// and a failure to read it means no Repo at all.
	if r.neutralise, err = driverOverrides(ctx, r.Top); err != nil {
		return nil, err
	}
	r.Confirmed = r.confirm(ctx)
	return &r, nil
}

// containsDir reports whether dir is top or lies below it. It compares file identity, not
// strings, so a case-insensitive file system or a symlinked spelling of dir is not refused.
func containsDir(top, dir string) bool {
	topInfo, err := os.Stat(top)
	if err != nil {
		return false
	}
	d, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	for {
		if info, err := os.Stat(d); err == nil && os.SameFile(info, topInfo) {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// confirm reports whether git itself vouches that Top is a worktree of this clone. A
// directory whose .git file merely names another clone's git directory is reported by
// rev-parse as a worktree of that clone, so rev-parse alone proves nothing. Either Top is the
// parent of the common directory (the main work tree), or `git worktree list`, run in the
// common directory, names it and the two files git keeps for a linked worktree agree: Top's
// .git file names <common>/worktrees/<id>, and <common>/worktrees/<id>/gitdir names
// Top/.git. Any failure is "not confirmed".
func (r *Repo) confirm(ctx context.Context) bool {
	if filepath.Base(r.CommonDir) == ".git" && samePath(r.Top, filepath.Dir(r.CommonDir)) {
		return true
	}
	listed, err := listWorktrees(ctx, r.CommonDir, r.neutralise)
	if err != nil {
		return false
	}
	for _, p := range listed {
		if samePath(r.Top, p) {
			return worktreeBound(r.Top, r.CommonDir, r.GitDir)
		}
	}
	return false
}

// listWorktrees returns the paths `git worktree list` reports for the clone whose common
// directory is commonDir, leaving out the entries git marks prunable.
func listWorktrees(ctx context.Context, commonDir string, neutralise [][2]string) ([]string, error) {
	out, err := run(ctx, commonDir, neutralise, nil, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(out), nil
}

// parseWorktrees reads the NUL-separated porcelain form. Fields end at a NUL and records at an
// empty field, so a directory name holding a newline cannot start a second entry the way it
// can in the newline-separated form.
func parseWorktrees(out []byte) []string {
	var paths []string
	cur, prunable := "", false
	flush := func() {
		if cur != "" && !prunable {
			paths = append(paths, cur)
		}
		cur, prunable = "", false
	}
	for _, f := range strings.Split(string(out), "\x00") {
		switch {
		case f == "":
			flush()
		case strings.HasPrefix(f, "worktree "):
			flush()
			cur = strings.TrimPrefix(f, "worktree ")
		case f == "prunable" || strings.HasPrefix(f, "prunable "):
			prunable = true
		}
	}
	flush()
	return paths
}

// worktreeBound reports whether top's .git file names gitDir, gitDir is an entry directly
// under <commonDir>/worktrees, and that entry's gitdir file names top/.git. Both files are
// read through confine, which accepts regular files only.
func worktreeBound(top, commonDir, gitDir string) bool {
	if !samePath(filepath.Dir(gitDir), filepath.Join(commonDir, "worktrees")) {
		return false
	}
	named, ok := readPointer(top, ".git", "gitdir:", top)
	if !ok || !samePath(named, gitDir) {
		return false
	}
	back, ok := readPointer(gitDir, "gitdir", "", gitDir)
	return ok && samePath(back, filepath.Join(top, ".git"))
}

// readPointer reads the one-line file name in dir, drops the keyword if given, and resolves a
// relative result against base.
func readPointer(dir, name, keyword, base string) (string, bool) {
	root, err := confine.Open(dir)
	if err != nil {
		return "", false
	}
	defer root.Close()
	data, err := root.ReadFile(name, 4096)
	if err != nil {
		return "", false
	}
	line := strings.TrimRight(string(data), "\r\n")
	if keyword != "" {
		rest, ok := strings.CutPrefix(line, keyword)
		if !ok {
			return "", false
		}
		line = strings.TrimSpace(rest)
	}
	if line == "" {
		return "", false
	}
	line = filepath.FromSlash(line)
	if !filepath.IsAbs(line) {
		line = filepath.Join(base, line)
	}
	return line, true
}

// samePath reports whether a and b name the same existing directory.
func samePath(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

// driverOverrides lists the overrides that empty every filter and diff driver setting the
// repository's config names. It fails closed: exit status 1 with an empty stderr is git's
// "no matching keys" and gives an empty list (the pattern needs a subsection, so the
// command-line keys set here, such as diff.external, never match); any other failure, including exit status 1
// with warnings (a config path git cannot read), is an error.
func driverOverrides(ctx context.Context, top string) ([][2]string, error) {
	names, err := run(ctx, top, nil, nil, "config", "--null", "--name-only", "--get-regexp", `^(filter|diff)\..+\.`)
	if err != nil {
		var ge *Error
		var ee *exec.ExitError
		if !(errors.As(err, &ge) && errors.As(err, &ee) && ee.ExitCode() == 1 && ge.Stderr == "") {
			return nil, fmt.Errorf("gitx: reading repository config: %w", err)
		}
		return nil, nil
	}
	var out [][2]string
	seen := map[string]bool{}
	for _, n := range bytes.Split(names, []byte{0}) {
		m := driverRe.FindStringSubmatch(string(n))
		if m == nil {
			continue
		}
		base := m[1] + "." + m[2]
		for _, setting := range []string{"clean", "smudge", "process", "textconv", "command"} {
			key := base + "." + setting
			if !seen[key] {
				seen[key] = true
				out = append(out, [2]string{key, ""})
			}
		}
		if req := base + ".required"; !seen[req] {
			seen[req] = true
			out = append(out, [2]string{req, "false"})
		}
	}
	return out, nil
}

// ObjectFormat is the repository's hash algorithm: "sha1" or "sha256".
func (r *Repo) ObjectFormat(ctx context.Context) (string, error) {
	return r.output(ctx, nil, "rev-parse", "--show-object-format")
}

// ResolveObject turns a full or short object id into the unique full id of an object of
// kind ("blob" or "commit"). An ambiguous short id is an error, never a guess.
func (r *Repo) ResolveObject(ctx context.Context, id, kind string) (string, error) {
	if !shaRe.MatchString(id) || (kind != "blob" && kind != "commit") {
		return "", fmt.Errorf("gitx: %q is not an object id", id)
	}
	full, err := r.output(ctx, nil, "rev-parse", "--verify", "--end-of-options", id+"^{"+kind+"}")
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && strings.Contains(ge.Stderr, "ambiguous") {
			return "", fmt.Errorf("%w: %s", ErrAmbiguous, id)
		}
		return "", fmt.Errorf("%w: %s", ErrUnknownObject, id)
	}
	return full, nil
}

// headRev resolves rev (a HEAD^{...} expression). An unborn HEAD, a branch with no commit
// yet, is the documented empty id with a nil error. Every other failure of git is an error,
// so two failed reads can never compare equal as "".
func (r *Repo) headRev(ctx context.Context, rev string) (string, error) {
	id, err := r.output(ctx, nil, "rev-parse", "-q", "--verify", rev)
	if err == nil {
		return id, nil
	}
	// With -q, git exits 1 with nothing on stderr when the revision does not resolve. That
	// is an unborn HEAD only if HEAD is a symbolic ref to a branch that does not exist.
	var ge *Error
	var ee *exec.ExitError
	if !(errors.As(err, &ge) && errors.As(err, &ee) && ee.ExitCode() == 1 && ge.Stderr == "") {
		return "", err
	}
	branch, serr := r.output(ctx, nil, "symbolic-ref", "-q", "HEAD")
	if serr != nil || branch == "" {
		return "", err
	}
	_, werr := r.git(ctx, nil, "show-ref", "--verify", "-q", branch)
	var we *Error
	var wee *exec.ExitError
	if errors.As(werr, &we) && errors.As(werr, &wee) && wee.ExitCode() == 1 && we.Stderr == "" {
		return "", nil
	}
	if werr != nil {
		return "", werr
	}
	return "", err // the branch exists but its commit does not resolve
}

// HeadCommit is the full HEAD commit id, or "" with a nil error before the first commit.
func (r *Repo) HeadCommit(ctx context.Context) (string, error) {
	return r.headRev(ctx, "HEAD^{commit}")
}

// HeadTree is the HEAD tree id, or "" with a nil error before the first commit.
func (r *Repo) HeadTree(ctx context.Context) (string, error) {
	return r.headRev(ctx, "HEAD^{tree}")
}

// HeadBlobs maps each path that is a blob at HEAD to its blob id.
func (r *Repo) HeadBlobs(ctx context.Context, paths []string) (map[string]string, error) {
	res := map[string]string{}
	if len(paths) == 0 {
		return res, nil
	}
	head, err := r.HeadCommit(ctx)
	if err != nil {
		return nil, err
	}
	if head == "" {
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
	return r.output(ctx, content, "hash-object", "--stdin", "--no-filters")
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
	branch, err := r.output(ctx, nil, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return branch
}

// OriginURL is remote.origin.url, else the first remote's URL, else "".
func (r *Repo) OriginURL(ctx context.Context) string {
	if origin, err := r.output(ctx, nil, "config", "--get", "remote.origin.url"); err == nil {
		return origin
	}
	out, err := r.git(ctx, nil, "remote")
	if err != nil {
		return ""
	}
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if first == "" {
		return ""
	}
	url, err := r.output(ctx, nil, "remote", "get-url", first) // first comes from git itself
	if err != nil {
		return ""
	}
	return url
}

// Dirty lists changed and untracked paths under pathspec, repository-relative.
func (r *Repo) Dirty(ctx context.Context, pathspec string) ([]string, error) {
	if err := r.checkExcludeFile(); err != nil {
		return nil, err
	}
	out, err := r.git(ctx, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=all", "--", pathspec)
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

// checkExcludeFile refuses a repository whose info/exclude is not a regular file. git reads
// that file on every status and no option skips it, so a FIFO there would block git (and its
// caller) for ever. core.excludesFile, which can name the same trap, is overridden with an
// empty value in safety instead. A missing file is normal. The check follows links, as git does.
func (r *Repo) checkExcludeFile() error {
	fi, err := os.Stat(filepath.Join(r.CommonDir, "info", "exclude"))
	if err != nil {
		return nil // absent or unreadable: git reports its own error for the latter
	}
	if !fi.Mode().IsRegular() {
		return ErrUnsafeExclude
	}
	return nil
}
