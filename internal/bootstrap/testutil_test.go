package bootstrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/gitx"
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

// newRepoWith makes a repository whose single commit holds files (slash path to content).
func newRepoWith(t *testing.T, files map[string]string) (string, *gitx.Repo) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	gitIn(t, dir, "-c", "init.defaultBranch=main", "init", "-q")
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "init")
	r, err := gitx.Discover(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, r
}

// memReader is an in-memory Reader for tests. It records every path it is asked to read.
type memReader struct {
	name  string
	files map[string]string
	tier  func(string) string
	reads []string
}

func (m *memReader) Name() string { return m.name }
func (m *memReader) Tier(p string) string {
	if m.tier != nil {
		return m.tier(p)
	}
	return TierOf(p)
}
func (m *memReader) List(context.Context) ([]Entry, error) {
	var es []Entry
	for p, c := range m.files {
		es = append(es, Entry{Source: m.name, Path: p, OID: "oid-" + p, Size: int64(len(c))})
	}
	return es, nil
}
func (m *memReader) Read(_ context.Context, e Entry, _ int64) ([]byte, error) {
	m.reads = append(m.reads, e.Path)
	return []byte(m.files[e.Path]), nil
}
