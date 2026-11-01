package publish

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/store"
)

type fixture struct {
	repoDir, dataDir string
	st               *store.Store
	repo             *confine.Root
	fp               string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{repoDir: t.TempDir(), dataDir: t.TempDir(), fp: "tree-1"}
	if err := os.MkdirAll(filepath.Join(f.repoDir, "project-memory", "wiki"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.writeRepo(t, "project-memory/wiki/b.md", "B0")
	f.open(t)
	return f
}

func (f *fixture) open(t *testing.T) {
	t.Helper()
	var err error
	id := store.Identity{ProjectID: "wkp_P", CommonDir: "/c/.git", Worktree: "/c"}
	f.st, err = store.Resolve(context.Background(), f.dataDir, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.Register(context.Background(), id); err != nil { // so the helper process finds this store
		t.Fatal(err)
	}
	if f.repo, err = confine.Open(f.repoDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.st.Close(); f.repo.Close() })
}

func (f *fixture) publisher(checkout string) *Publisher {
	return &Publisher{DB: f.st.DB, Store: f.st.Root, Repo: f.repo, CheckoutID: checkout,
		LockPath: filepath.Join(f.st.Dir, "locks", "publish.lock"), LogPath: "project-memory/log.md",
		Fingerprint: func(context.Context) (string, error) { return f.fp, nil }, AbandonAfter: time.Nanosecond, Now: time.Now}
}

func (f *fixture) writeRepo(t *testing.T, rel, s string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.repoDir, filepath.FromSlash(rel)), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) readRepo(t *testing.T, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.repoDir, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (f *fixture) logLines(t *testing.T, jobID string) int {
	t.Helper()
	s, _ := f.readRepo(t, "project-memory/log.md")
	return strings.Count(s, "job="+jobID)
}

func (f *fixture) jobState(t *testing.T, jobID string) string {
	t.Helper()
	var s string
	if err := f.st.DB.QueryRow(`SELECT state FROM jobs WHERE job_id = ?`, jobID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func standardOps() []Op {
	return []Op{
		{Target: "project-memory/wiki/a.md", OldHash: Absent, Content: []byte("A1")},
		{Target: "project-memory/wiki/b.md", OldHash: HashContent([]byte("B0")), Content: []byte("B1")},
	}
}

func symlinksAvailable() bool {
	d, err := os.MkdirTemp("", "wikory-symlink-probe")
	if err != nil {
		return false
	}
	defer os.RemoveAll(d)
	return os.Symlink(filepath.Join(d, "x"), filepath.Join(d, "y")) == nil
}

func (f *fixture) writeAbs(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) readAbs(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

func (f *fixture) symlinkRepo(t *testing.T, target, rel string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(f.repoDir, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}
