package app

import (
	"context"
	"fmt"
	"time"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
)

// progressNow is the clock progress reporting uses; a test replaces it to make quick work look slow.
var progressNow = time.Now

// progressTTY decides whether stderr is a terminal; a test replaces it to see the terminal form.
var progressTTY = IsTTY

// newBootstrapProgress is the reporter for next and finish. It writes to stderr (JSON events in
// --json mode), so stdout stays one envelope.
func newBootstrapProgress(env *Env) *Progress {
	return NewProgress(env.IO.Err, env.JSON, progressTTY(env.IO.Err), progressNow)
}

// verifyPrepDue reports whether the next PrepareVerify will do real work, and for how many
// components. It mirrors PrepareVerify's own conditions; if they drift apart the only effect is
// a progress line shown or not shown.
func verifyPrepDue(ctx context.Context, runs *bootstrap.Runs, runID string) (bool, int) {
	st, err := runs.Status(ctx, runID)
	if err != nil || st.Run.State != bootstrap.StateRunning || len(st.Items["verify"]) > 0 || st.Items["extract"]["pending"]+st.Items["extract"]["leased"] > 0 {
		return false, 0
	}
	comps, err := runs.Components(ctx, runID)
	if err != nil {
		return false, 0
	}
	n := bootstrap.CountInspected(comps)
	return n > 0, n
}

// prepareVerifyWithProgress runs PrepareVerify, announcing it first when it has work to do:
// next is otherwise a quick call, and this is the one step in it that reads and digests files.
func prepareVerifyWithProgress(ctx context.Context, env *Env, p *project, runs *bootstrap.Runs, runID string) error {
	prog := newBootstrapProgress(env)
	due, n := verifyPrepDue(ctx, runs, runID)
	if due {
		prog.BeginStage(1, 1, "prepare-verification", n, time.Duration(n)*200*time.Millisecond,
			fmt.Sprintf("Preparing verification work for %d component(s). Safe to leave running; it resumes if interrupted.", n))
	}
	if _, err := bootstrap.PrepareVerify(ctx, runs, bootstrap.WorkFiles{Root: p.Store.Root, CheckoutID: p.Checkout.ID}, runID); err != nil {
		return err
	}
	if due {
		prog.Step(n)
		prog.End()
	}
	return nil
}
