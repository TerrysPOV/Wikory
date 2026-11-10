package bootstrap

import (
	"context"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/ids"
)

func verifyItems(t *testing.T, r *Runs, runID string) int {
	t.Helper()
	st, err := r.Status(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range st.Items["verify"] {
		n += c
	}
	return n
}

// A next call that read the run as running can reach enqueueVerify after finish has moved the run
// on. Once the run is finishing (or done) nothing may add work to it: those rows could never be
// handed out or committed, and a later finish would refuse for ever because of them.
func TestEnqueueVerifyInsertsNothingIntoARunThatIsNotRunning(t *testing.T) {
	ctx := context.Background()
	for _, state := range []string{StateFinishing, StateDone} {
		r, _, run := extractedRig(t)
		if state == StateDone {
			if err := r.Transition(ctx, run.ID, StateFinishing, StateRunning); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Transition(ctx, run.ID, state, StateRunning, StateFinishing); err != nil {
			t.Fatal(err)
		}
		spec := ItemSpec{Stage: "verify", Ref: "comp:internal/queue", InputHash: ids.SHA256Hex([]byte("late"))}
		if _, err := r.enqueueVerify(ctx, run.ID, []ItemSpec{spec}, false); err != nil {
			t.Fatalf("%s: a late enqueue is not an error, it simply adds nothing: %v", state, err)
		}
		if n := verifyItems(t, r, run.ID); n != 0 {
			t.Fatalf("%s: %d verify item(s) were added to a run that is no longer running", state, n)
		}
	}
	// The same call on a running run still queues the item.
	r, _, run := extractedRig(t)
	spec := ItemSpec{Stage: "verify", Ref: "comp:internal/queue", InputHash: ids.SHA256Hex([]byte("on time"))}
	if _, err := r.enqueueVerify(ctx, run.ID, []ItemSpec{spec}, false); err != nil || verifyItems(t, r, run.ID) != 1 {
		t.Fatalf("a running run takes the item: %v", err)
	}
}

// The count a caller is told is the number of rows that went in, not the number it asked for.
func TestEnqueueVerifyReportsHowManyItemsItQueued(t *testing.T) {
	ctx := context.Background()
	r, _, run := extractedRig(t)
	spec := ItemSpec{Stage: "verify", Ref: "comp:internal/queue", InputHash: ids.SHA256Hex([]byte("x"))}
	if n, err := r.enqueueVerify(ctx, run.ID, []ItemSpec{spec}, false); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if n, err := r.enqueueVerify(ctx, run.ID, []ItemSpec{spec}, false); err != nil || n != 0 {
		t.Fatalf("a component that already has an item adds nothing: %d %v", n, err)
	}
	if err := r.Transition(ctx, run.ID, StateFinishing, StateRunning); err != nil {
		t.Fatal(err)
	}
	other := ItemSpec{Stage: "verify", Ref: "comp:internal/other", InputHash: ids.SHA256Hex([]byte("y"))}
	if n, err := r.enqueueVerify(ctx, run.ID, []ItemSpec{other}, false); err != nil || n != 0 {
		t.Fatalf("a finishing run takes nothing: %d %v", n, err)
	}
}

// VerifyOwed is the condition PrepareVerify acts on, readable without acting and in any state:
// no verify item exists yet and an inspected component has claims to judge.
func TestVerifyOwedCountsInspectedComponentsWithClaimsWhenNoVerifyItemExists(t *testing.T) {
	ctx := context.Background()
	r, w, run := extractedRig(t)
	if n, err := VerifyOwed(ctx, r, run.ID); err != nil || n != 1 {
		t.Fatalf("one inspected component has claims and no verify item: %d %v", n, err)
	}
	if err := r.Transition(ctx, run.ID, StateFinishing, StateRunning); err != nil {
		t.Fatal(err)
	}
	if n, err := VerifyOwed(ctx, r, run.ID); err != nil || n != 1 {
		t.Fatalf("the answer does not depend on the run state: %d %v", n, err)
	}
	if err := r.Transition(ctx, run.ID, StateRunning, StateFinishing); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareVerify(ctx, r, w, run.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := VerifyOwed(ctx, r, run.ID); err != nil || n != 0 {
		t.Fatalf("once verify items exist nothing is owed: %d %v", n, err)
	}
}

// A run with nothing to judge owes nothing.
func TestVerifyOwedIsZeroWhenNoClaimNeedsJudging(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run, err := r.Create(ctx, planWith(0), specsN(1))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := VerifyOwed(ctx, r, run.ID); err != nil || n != 0 {
		t.Fatalf("a run with nothing to judge owes nothing: %d %v", n, err)
	}
}

func TestEnterFinishingReportsWhetherThisCallMadeTheMove(t *testing.T) {
	ctx := context.Background()
	r, _, run := extractedRig(t)
	if moved, err := r.EnterFinishing(ctx, run.ID); err != nil || !moved {
		t.Fatalf("a running run is moved by this call: %v %v", moved, err)
	}
	if moved, err := r.EnterFinishing(ctx, run.ID); err != nil || moved {
		t.Fatalf("a run that is already finishing was not moved by this call: %v %v", moved, err)
	}
	if err := r.LeaveFinishing(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Get(ctx, run.ID); got.State != StateRunning {
		t.Fatalf("%+v", got)
	}
	if err := r.Transition(ctx, run.ID, StateDone, StateRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EnterFinishing(ctx, run.ID); err == nil {
		t.Fatal("a finished run cannot go back to finishing")
	}
}
