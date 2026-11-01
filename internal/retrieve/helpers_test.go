package retrieve

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Resolve(context.Background(), t.TempDir(), store.Identity{ProjectID: "wkp_P", CommonDir: "/c/.git", Worktree: "/c"}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func writePage(t *testing.T, repoDir, name, content string) {
	t.Helper()
	p := filepath.Join(repoDir, "project-memory", "wiki", filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openRoot(t *testing.T, dir string) *confine.Root {
	t.Helper()
	r, err := confine.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func ftsCount(t *testing.T, st *store.Store, checkout, relpath string) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(`SELECT count(*) FROM fts WHERE checkout_id = ? AND relpath = ?`, checkout, relpath).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const queuePage = `---
type: component
title: Job queue
---
# Job queue
## Acknowledgement
<!-- claim kind=current-impl status=unverified ev=src/queue.go dep=src/queue.go -->
enqueueJob acknowledges only after the handler returns.
`

func writeRaw(t *testing.T, repoDir, rel, content string) {
	t.Helper()
	p := filepath.Join(repoDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
