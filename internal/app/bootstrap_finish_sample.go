package app

import (
	"context"
	"fmt"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
)

// sampleFinishReport is what finish answers for a run whose sample has settled.
type sampleFinishReport struct {
	RunID     string            `json:"run_id"`
	State     string            `json:"state"`
	Sample    bootstrap.Measure `json:"sample"`
	Published int               `json:"pages_published"`
}

// finishSample is finish on a run planned with a sample that has not been confirmed for the full
// run. A sample publishes nothing and leaves the run as it is: once the sample has settled, finish
// reports what it measured (tokens, excerpt acceptance, verdict counts and the projections);
// while it is still running, finish first queues the sample's verify work, as next would, and
// otherwise says what is left.
func finishSample(ctx context.Context, p *project, runs *bootstrap.Runs, run bootstrap.Run) (Result, error) {
	if run.State == bootstrap.StateSampling {
		wf := bootstrap.WorkFiles{Root: p.Store.Root, CheckoutID: p.Checkout.ID}
		n, err := bootstrap.PrepareVerify(ctx, runs, wf, run.ID)
		if err != nil {
			return Result{}, err
		}
		if n > 0 {
			return Result{}, fmt.Errorf("%d verify item(s) were just created for the sample; run wikory bootstrap next to hand them out and submit the verdicts; the sample then settles", n)
		}
		if run, err = runs.Get(ctx, run.ID); err != nil {
			return Result{}, err
		}
		if run.State == bootstrap.StateSampling {
			return Result{}, fmt.Errorf("the sample is still running; hand its items out with wikory bootstrap next and submit them, and it settles to awaiting-confirm. A sample publishes nothing")
		}
	}
	m, err := runs.Measure(ctx, run.ID)
	if err != nil {
		return Result{}, err
	}
	rep := sampleFinishReport{RunID: run.ID, State: run.State, Sample: m}
	text := fmt.Sprintf("Run %s: the sample is done and nothing was published (a sample publishes no pages).\n%s"+
		"The full run starts only with wikory bootstrap confirm --run %s --plan-hash HASH, after the person's yes; finish then builds the baseline.\n",
		run.ID, sampleText(m), run.ID)
	return Result{Data: rep, Text: text}, nil
}
