package app

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/publish"
)

// runStateOf reads a run's state straight from the store.
func runStateOf(t *testing.T, c *cli, runID string) string {
	t.Helper()
	db := openTestDB(t, c)
	defer db.Close()
	var s string
	if err := db.QueryRow(`SELECT state FROM bs_runs WHERE run_id=?`, runID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// plantPendingVerifyItem adds a pending verify item to a run, as a next call that read the run
// as running would have done a moment before finish moved it on.
func plantPendingVerifyItem(t *testing.T, c *cli, runID string) {
	t.Helper()
	db := openTestDB(t, c)
	defer db.Close()
	var checkout string
	if err := db.QueryRow(`SELECT checkout_id FROM bs_runs WHERE run_id=?`, runID).Scan(&checkout); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bs_items(item_id,run_id,checkout_id,stage,ref,input_hash,state) VALUES('itm_late',?,?,'verify','comp:internal/late','h','pending')`, runID, checkout); err != nil {
		t.Fatal(err)
	}
}

// withKillPoint installs a hook for the duration of the test and reports how often it fired.
func withKillPoint(t *testing.T, step string, fn func()) *int {
	t.Helper()
	old := killPoint
	fired := 0
	killPoint = func(s string) {
		if s == step {
			fired++
			fn()
		}
	}
	t.Cleanup(func() { killPoint = old })
	return &fired
}

// The last extract submit can land after finish's PrepareVerify returned 0 (extract still open at
// that moment) and before its next look at the items. Finish then sees extraction closed and no
// verify items, and without a second look it would publish a baseline nobody verified.
func TestFinishDoesNotPublishWhenTheLastExtractLandsAfterThePrepareCheck(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	run := decodeData[planData](t, pe).RunID
	forceState(t, c, run, "running")
	if _, code, _ := runBootstrapJSON(t, c, dir, "next", "--run", run, "--max", "50"); code != ExitOK {
		t.Fatal("next")
	}
	fired := withKillPoint(t, "prepared", func() {
		// The last extract submit commits here: the items close and a claim for a checked component appears.
		forceItemsClosed(t, c, run)
		plantClaimRow(t, c, run, "The internal/queue package acknowledges after the handler returns.")
	})
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if *fired != 1 {
		t.Fatalf("the interleaving was not reproduced: the hook fired %d times", *fired)
	}
	if code == ExitOK || e.OK || !strings.Contains(e.Error.Message, "verify") {
		t.Fatalf("finish must not publish a baseline that was never verified: %d %+v", code, e)
	}
	if _, err := os.Stat(filepath.Join(dir, "project-memory", "wiki", "bootstrap")); err == nil {
		t.Fatal("nothing may have been published")
	}
	if got := runStateOf(t, c, run); got != "running" {
		t.Fatalf("a refused finish leaves the run running so that next can still hand out the verify work, got %s", got)
	}
	// With the hook gone the work can proceed: next prepares and hands out the verify item.
	*fired = 0
	killPoint = func(string) {}
	ne, _, _ := runBootstrapJSON(t, c, dir, "next", "--run", run)
	if d := decodeData[struct {
		Items []struct{} `json:"items"`
	}](t, ne); len(d.Items) != 1 {
		t.Fatalf("one verify item for the checked component: %+v", ne)
	}
	forceItemsClosed(t, c, run)
	if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code != ExitOK || !e.OK {
		t.Fatalf("a later finish after the items close succeeds: %d %+v", code, e)
	}
}

// A verify row inserted after finish looked at the items is caught by the look after the move to
// finishing, and the refusal puts the run back to running: nothing else could close those items
// from finishing, so a run left there would refuse every later finish for ever.
func TestARefusalAfterEnteringFinishingLeavesTheRunRunning(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	fired := withKillPoint(t, "checked", func() { plantPendingVerifyItem(t, c, run) })
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if *fired != 1 {
		t.Fatalf("the hook fired %d times", *fired)
	}
	if code == ExitOK || e.OK || !strings.Contains(e.Error.Message, "verify item(s) are still open") {
		t.Fatalf("finish refuses with the usual message: %d %+v", code, e)
	}
	if _, err := os.Stat(filepath.Join(dir, "project-memory", "wiki", "bootstrap")); err == nil {
		t.Fatal("nothing may have been published")
	}
	if got := runStateOf(t, c, run); got != "running" {
		t.Fatalf("the refusal must leave the run running, got %s", got)
	}
	killPoint = func(string) {}
	forceItemsClosed(t, c, run)
	if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code != ExitOK || !e.OK {
		t.Fatalf("finish after the items close: %d %+v", code, e)
	}
}

// A run that was already finishing when this call began (an earlier finish failed after the
// move) is not this call's to put back: only a call that made the move undoes it.
func TestARefusalLeavesAFinishingRunFinishingWhenThisCallDidNotMoveIt(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	forceState(t, c, run, "finishing")
	fired := withKillPoint(t, "checked", func() { plantPendingVerifyItem(t, c, run) })
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if *fired != 1 || code == ExitOK || e.OK {
		t.Fatalf("fired %d: %d %+v", *fired, code, e)
	}
	if got := runStateOf(t, c, run); got != "finishing" {
		t.Fatalf("this call did not make the move, so it must not undo it, got %s", got)
	}
}

func TestFinishRefusalNamesBothStatesThatCanBeFinished(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	// A done run is not refused: finish reprints its stored report (final review A8).
	forceState(t, c, run, "abandoned")
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code == ExitOK || e.OK || !strings.Contains(e.Error.Message, "running or finishing") {
		t.Fatalf("the refusal names both states that can be finished: %d %+v", code, e)
	}
}

// A page created between the plan and the write is refused by the publisher before anything is
// applied. The message says so (without the publisher's own sentence about earlier operations
// being applied, which would be untrue here) and says what to do.
func TestFinishExplainsAPageEditedAfterPlanningAndAFreshFinishReportsItAsAConflict(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	overview := filepath.Join(dir, "project-memory", "wiki", "bootstrap", "overview.md")
	fired := withKillPoint(t, "planned", func() {
		if err := os.MkdirAll(filepath.Dir(overview), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(overview, []byte("A person wrote this first.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if *fired != 1 || code == ExitOK || e.OK {
		t.Fatalf("fired %d: %d %+v", *fired, code, e)
	}
	msg := e.Error.Message
	if !strings.Contains(msg, "nothing was applied") || !strings.Contains(msg, "wikory bootstrap finish") || !strings.Contains(msg, "conflict") {
		t.Fatalf("the message says nothing was applied and to run finish again, which reports the edit as a conflict: %q", msg)
	}
	if strings.Contains(msg, "may already be applied") {
		t.Fatalf("a revalidation refusal applied nothing, so it must not say operations may be applied: %q", msg)
	}
	killPoint = func(string) {}
	e2, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK {
		t.Fatalf("%d %+v", code, e2)
	}
	conflicts := 0
	for _, p := range decodeData[finishReport](t, e2).Pages {
		if strings.HasSuffix(p.Path, "overview.md") && p.Outcome == "conflict" {
			conflicts++
		}
	}
	if conflicts != 1 || readFile(t, overview) != "A person wrote this first.\n" {
		t.Fatalf("the second finish reports the page as a conflict and leaves it alone (%d)", conflicts)
	}
}

func TestDescribePublishConflictTellsARefusedJobFromAnAppliedOne(t *testing.T) {
	cause := publish.ErrConflict
	refused := describePublishConflict(cause, "refused", 0).Error()
	if !strings.Contains(refused, "nothing was applied") || strings.Contains(refused, "may already be applied") || strings.Contains(refused, "earlier") {
		t.Fatalf("a refused job applied nothing: %q", refused)
	}
	later := describePublishConflict(cause, "refused", 1).Error()
	if !strings.Contains(later, "earlier") || !strings.Contains(later, "already written") {
		t.Fatalf("a refusal after an earlier job says that job's pages are already written: %q", later)
	}
	applied := describePublishConflict(cause, "conflict", 0).Error()
	if !strings.Contains(applied, "may already be applied") || strings.Contains(applied, "nothing was applied") {
		t.Fatalf("a conflict found while applying may have applied earlier operations: %q", applied)
	}
	for _, m := range []string{refused, later, applied} {
		if !strings.Contains(m, "wikory bootstrap finish") {
			t.Fatalf("every message says to run finish again: %q", m)
		}
	}
	if !errors.Is(describePublishConflict(cause, "refused", 0), publish.ErrConflict) {
		t.Fatal("the cause stays reachable with errors.Is")
	}
}

// A page that has grown past the read limit is not ours; it is a conflict, not a reason to abort.
func TestFinishTreatsAnOversizePageAsAConflictNotAnAbort(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	overview := filepath.Join(dir, "project-memory", "wiki", "bootstrap", "overview.md")
	if err := os.MkdirAll(filepath.Dir(overview), 0o755); err != nil {
		t.Fatal(err)
	}
	big := []byte(strings.Repeat("a long line of notes\n", 5<<20/21+10))
	if len(big) <= 4<<20 {
		t.Fatalf("the page must exceed the read limit: %d", len(big))
	}
	if err := os.WriteFile(overview, big, 0o644); err != nil {
		t.Fatal(err)
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK || !e.OK {
		t.Fatalf("an oversize page must not abort finish: %d %+v", code, e)
	}
	conflicts := 0
	for _, p := range decodeData[finishReport](t, e).Pages {
		if strings.HasSuffix(p.Path, "overview.md") && p.Outcome == "conflict" {
			conflicts++
		}
	}
	after, _ := os.ReadFile(overview)
	if conflicts != 1 || len(after) != len(big) {
		t.Fatalf("the oversize page is reported as a conflict and left as it was (%d conflicts, %d bytes)", conflicts, len(after))
	}
}

// A failure after some progress was shown must end the progress line, or the next thing written
// to a terminal starts in the middle of it.
func TestAFailedFinishEndsTheProgressLineOnATerminal(t *testing.T) {
	oldNow, oldTTY, oldMax := progressNow, progressTTY, maxOpsPerJob
	progressNow = steppingClock(3 * time.Second)
	progressTTY = func(io.Writer) bool { return true }
	maxOpsPerJob = 2
	t.Cleanup(func() { progressNow, progressTTY, maxOpsPerJob = oldNow, oldTTY, oldMax })
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	fired := withKillPoint(t, "after-job-0", func() {
		// A person creates every fixed page, so the next job's pages are no longer free to write.
		for _, n := range []string{"overview", "architecture", "constraints", "known-issues", "unfinished-work"} {
			if err := os.WriteFile(filepath.Join(dir, "project-memory", "wiki", "bootstrap", n+".md"), []byte("mine\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	})
	_, errOut, code := c.run(dir, "bootstrap", "finish", "--run", run)
	if *fired != 1 || code == ExitOK {
		t.Fatalf("the second job should have been refused (fired %d, code %d):\n%s", *fired, code, errOut)
	}
	at := strings.Index(errOut, "wikory bootstrap:")
	if !strings.Contains(errOut, "wikory: stage 2 of 2: publish") || at < 1 || errOut[at-1] != '\n' {
		t.Fatalf("the error must start on a fresh line, not on the end of the progress line:\n%q", errOut)
	}
}
