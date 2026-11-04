package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A component page says how many files the component has and whether it is an entry point. Both
// are decided at plan time and must survive the store to the page finish writes.
func TestFinishedComponentPageShowsItsFileCountAndEntryPoint(t *testing.T) {
	dir, c, run := finishedRunInputsWith(t, map[string]string{"internal/queue/main.go": "package main\n\nfunc main() {}\n"})
	forceItemsClosed(t, c, run)
	if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code != ExitOK || !e.OK {
		t.Fatalf("finish: %d %+v", code, e)
	}
	pages, err := filepath.Glob(filepath.Join(dir, "project-memory", "wiki", "bootstrap", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	var page string
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "Component `internal/queue`: rank") {
			page = string(b)
		}
	}
	if page == "" {
		t.Fatalf("no component page for internal/queue among %v", pages)
	}
	if !strings.Contains(page, "2 files, entry point.") {
		t.Fatalf("the component page must give the file count and the entry-point mark:\n%s", page)
	}
}
