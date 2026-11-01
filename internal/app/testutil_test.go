package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newGitRepo makes a repository with one commit and an origin remote.
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	gitIn(t, dir, "-c", "init.defaultBranch=main", "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "README.md")
	gitIn(t, dir, "commit", "-qm", "init")
	gitIn(t, dir, "remote", "add", "origin", "git@github.com:demo/repo.git")
	return dir
}

type cli struct {
	t       *testing.T
	dataDir string
	stdin   string
	extra   map[string]string
}

func newCLI(t *testing.T) *cli { return &cli{t: t, dataDir: t.TempDir(), extra: map[string]string{}} }

func (c *cli) run(dir string, args ...string) (stdout, stderr string, code int) {
	var out, errb bytes.Buffer
	env := &Env{IO: IO{In: strings.NewReader(c.stdin), Out: &out, Err: &errb}, Version: "test",
		Getenv: func(k string) string {
			if k == "WIKORY_DATA_DIR" {
				return c.dataDir
			}
			if v, ok := c.extra[k]; ok {
				return v
			}
			return os.Getenv(k)
		}, Cwd: dir}
	code = RunEnv(args, env)
	return out.String(), errb.String(), code
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
