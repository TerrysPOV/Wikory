package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A --source inside project-memory/ would read Wikory's own pages back as source material, so it
// is refused, however the path is written. One elsewhere inside the repository is allowed with a
// notice, because its files are read twice.
func TestPlanRefusesASourceInsideProjectMemoryAndWarnsForOneInsideTheRepository(t *testing.T) {
	dir, c := bootstrapRepo(t)
	wiki := filepath.Join(dir, "project-memory", "wiki")
	if err := os.MkdirAll(wiki, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []string{"old=" + wiki, "old=project-memory", "old=" + filepath.Join(dir, "docs", "..", "project-memory")} {
		e, code, _ := runBootstrapJSON(t, c, dir, "plan", "--source", spec)
		if code != ExitUsage || e.OK || !strings.Contains(e.Error.Message, "Wikory's own output") {
			t.Errorf("--source %s must be refused: %d %+v", spec, code, e)
		}
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "plan", "--source", "docs="+filepath.Join(dir, "docs"))
	if code != ExitOK || !strings.Contains(strings.Join(e.Notices, " "), "inside the repository") {
		t.Fatalf("a source inside the repository is allowed with a notice: %d %+v", code, e)
	}
}

// The plan names every --source directory it will read, so the person's yes covers them.
func TestPlanShowsEverySourceDirectory(t *testing.T) {
	dir, c := bootstrapRepo(t)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(outside, "notes.md"), []byte("internal/queue notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := c.run(dir, "bootstrap", "plan", "--source", "notes="+outside)
	if code != ExitOK || !strings.Contains(out, "--source notes="+outside) {
		t.Fatalf("the plan text must name each source directory:\n%s", out)
	}
	e, _, _ := runBootstrapJSON(t, c, dir, "plan", "--source", "notes="+outside)
	if roots := decodeData[struct {
		Roots map[string]string `json:"roots"`
	}](t, e).Roots; roots["notes"] != outside {
		t.Fatalf("the plan JSON must name each source directory: %v", roots)
	}
}
