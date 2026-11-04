package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
)

// jsonEqual compares two JSON documents by value.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

// The report finish prints is stored with the run when it is marked done; status and a second
// finish show exactly that report, outcomes included.
func TestTheFinishReportIsStoredWithTheRunAndReprinted(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	first, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK || !first.OK {
		t.Fatalf("finish: %d %+v", code, first)
	}
	printed, _ := json.Marshal(first.Data)
	st, _, _ := runBootstrapJSON(t, c, dir, "status", "--run", run)
	stored := decodeData[struct {
		Coverage json.RawMessage `json:"coverage"`
	}](t, st).Coverage
	if len(stored) == 0 || !jsonEqual(t, stored, printed) {
		t.Fatalf("status shows the stored report:\nstatus %s\nfinish %s", stored, printed)
	}
	again, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	reprinted, _ := json.Marshal(again.Data)
	if code != ExitOK || !jsonEqual(t, reprinted, printed) {
		t.Fatalf("finish on a done run reprints the same report: %d\n%s\n%s", code, reprinted, printed)
	}
	if text, _, code := c.run(dir, "bootstrap", "status", "--run", run); code != ExitOK || !bytes.Contains([]byte(text), []byte("End marker:")) {
		t.Fatalf("status text includes the stored report:\n%s", text)
	}
}

// A run whose report cannot be built is not marked done: it stays finishing, so finish can be
// run again once the cause is fixed.
func TestAFinishWhoseReportCannotBeBuiltLeavesTheRunFinishing(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	old := buildCoverage
	buildCoverage = func(context.Context, *bootstrap.Runs, string, []bootstrap.PageOutcome, time.Time) (bootstrap.Coverage, error) {
		return bootstrap.Coverage{}, errors.New("report store unavailable")
	}
	t.Cleanup(func() { buildCoverage = old })
	if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code == ExitOK || e.OK {
		t.Fatalf("finish must fail when the report cannot be built: %+v", e)
	}
	if got := stateOf(t, c, dir, run); got != "finishing" {
		t.Fatalf("a run without its report must not be done; state %q", got)
	}
	buildCoverage = old
	if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code != ExitOK || !e.OK {
		t.Fatalf("finish again completes: %d %+v", code, e)
	}
}
