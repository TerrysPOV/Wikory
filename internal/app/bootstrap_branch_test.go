package app

import (
	"strings"
	"testing"
)

// finish writes pages into the working tree, so it must run on the branch the run was planned on,
// not merely at the same commit: another branch at that commit is someone else's work.
func TestFinishRefusesAnotherBranchAtTheSameCommit(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	gitIn(t, dir, "checkout", "-q", "-b", "elsewhere")
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitUsage || e.OK || !strings.Contains(e.Error.Message, `planned on branch "main"`) || !strings.Contains(e.Error.Message, `"elsewhere"`) {
		t.Fatalf("finish on another branch must be refused and name both branches: %d %+v", code, e)
	}
	if n := pagesRecorded(t, c); n != 0 {
		t.Fatalf("a refused finish writes nothing: %d pages recorded", n)
	}
	gitIn(t, dir, "checkout", "-q", "main")
	if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code != ExitOK || !e.OK {
		t.Fatalf("back on the planned branch, finish completes: %d %+v", code, e)
	}
}
