package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type runState struct {
	Run struct {
		State string `json:"state"`
	} `json:"run"`
}

func stateOf(t *testing.T, c *cli, dir, run string) string {
	t.Helper()
	st, _, _ := runBootstrapJSON(t, c, dir, "status", "--run", run)
	return decodeData[runState](t, st).Run.State
}

// A finish killed after its first publish job leaves the run finishing with pages on disk that
// were never recorded as committed. If the repository then moves, that run can never finish: it
// must say how to get out (abandon), and the next bootstrap must recognise the pages the killed
// job wrote as Wikory's own and refresh them, never file them as a person's edits.
func TestPagesAKilledFinishWroteAreStillOursAfterTheRepositoryMoves(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	killFinishAt(t, dir, c, run, "after-job-0")
	written := pageNames(t, dir)
	if len(written) == 0 {
		t.Fatal("the first job wrote no page, so the test proves nothing")
	}
	if err := os.WriteFile(filepath.Join(dir, "later.go"), []byte("package later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "later.go")
	gitIn(t, dir, "commit", "-qm", "the repository moves on")

	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code == ExitOK || e.OK || !strings.Contains(e.Error.Message, "wikory bootstrap abandon --run "+run) {
		t.Fatalf("a finishing run whose repository moved must name the way out: %d %+v", code, e)
	}
	if e, code, _ := runBootstrapJSON(t, c, dir, "abandon", "--run", run); code != ExitOK || !e.OK {
		t.Fatalf("abandon: %d %+v", code, e)
	}
	if got := stateOf(t, c, dir, run); got != "abandoned" {
		t.Fatalf("state after abandon = %q", got)
	}

	pe, code, _ := runBootstrapJSON(t, c, dir, "plan")
	if code != ExitOK {
		t.Fatalf("plan after abandon: %d %+v", code, pe)
	}
	next := decodeData[planData](t, pe).RunID
	forceState(t, c, next, "running")
	forceItemsClosed(t, c, next)
	fe, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", next)
	if code != ExitOK || !fe.OK {
		t.Fatalf("finish of the new run: %d %+v", code, fe)
	}
	outcome := map[string]string{}
	for _, p := range decodeData[finishPages](t, fe).Pages {
		outcome[filepath.Base(p.Path)] = p.Outcome
	}
	for _, name := range written {
		if outcome[name] == "conflict" {
			t.Fatalf("%s: a page the killed finish wrote was taken for a person's edit", name)
		}
	}
	refreshed := 0
	for _, name := range written {
		if outcome[name] == "written" {
			refreshed++
		}
	}
	if refreshed == 0 {
		t.Fatalf("the pages the killed job wrote are refreshed for the new commit: %v", outcome)
	}
}

// Abandon drops a run and touches no page: what an earlier bootstrap recorded stays recorded, and
// the abandoned run can never be handed out, confirmed or finished.
func TestAbandonLeavesRecordedPagesAloneAndTheRunCannotBeUsed(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code != ExitOK || !e.OK {
		t.Fatalf("first finish: %d %+v", code, e)
	}
	before := recordedPages(t, c)
	files := map[string]string{}
	for _, name := range pageNames(t, dir) {
		files[name] = readFile(t, filepath.Join(dir, "project-memory", "wiki", "bootstrap", name))
	}
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	pd := decodeData[planData](t, pe)
	forceState(t, c, pd.RunID, "running")
	if e, code, _ := runBootstrapJSON(t, c, dir, "abandon", "--run", pd.RunID); code != ExitOK || !e.OK {
		t.Fatalf("abandon: %d %+v", code, e)
	}
	after := recordedPages(t, c)
	if len(after) != len(before) {
		t.Fatalf("abandon changed the recorded pages: %v -> %v", before, after)
	}
	for p, h := range before {
		if after[p] != h {
			t.Fatalf("abandon changed the recorded hash of %s", p)
		}
	}
	for name, text := range files {
		if readFile(t, filepath.Join(dir, "project-memory", "wiki", "bootstrap", name)) != text {
			t.Fatalf("abandon changed %s", name)
		}
	}
	for _, args := range [][]string{
		{"next", "--run", pd.RunID},
		{"finish", "--run", pd.RunID},
		{"confirm", "--run", pd.RunID, "--plan-hash", pd.PlanHash},
		{"abandon", "--run", pd.RunID},
	} {
		if e, code, _ := runBootstrapJSON(t, c, dir, args...); code == ExitOK || e.OK {
			t.Errorf("%v on an abandoned run must be refused: %+v", args, e)
		}
	}
	if e, code, _ := runBootstrapJSON(t, c, dir, "abandon", "--run", run); code == ExitOK || e.OK {
		t.Errorf("a done run cannot be abandoned: %+v", e)
	}
}
