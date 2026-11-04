package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
)

type abandonReport struct {
	RunID    string `json:"run_id"`
	Previous string `json:"previous_state"`
	State    string `json:"state"`
}

// bootstrapAbandon drops a run that has not finished, so a stuck run (for example one left
// finishing when the repository then moved) is never in the way. It changes no page and promotes
// nothing; the next bootstrap still recognises the pages the run wrote as Wikory's own. A done
// or already abandoned run is refused.
func bootstrapAbandon(ctx context.Context, env *Env, args []string) (Result, error) {
	p, runs, fl, err := openRun(ctx, env, args, map[string]bool{"run": true})
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	id := oneFlag(fl, "run")
	if id == "" {
		return Result{}, &UsageError{Msg: "usage: wikory bootstrap abandon --run RUN"}
	}
	run, err := runs.Get(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if err := runs.Abandon(ctx, id); err != nil {
		if errors.Is(err, bootstrap.ErrState) {
			return Result{}, &UsageError{Msg: fmt.Sprintf("the run is %s; only a run that has not finished can be abandoned", safeState(ctx, runs, id, run.State))}
		}
		return Result{}, err
	}
	rep := abandonReport{RunID: id, Previous: run.State, State: bootstrap.StateAbandoned}
	return Result{Data: rep, Text: fmt.Sprintf("Run %s (%s) is abandoned. Nothing was published or deleted; plan again to start a new run.\n", id, run.State)}, nil
}

// safeState is the run's current state for a message, falling back to the one read earlier.
func safeState(ctx context.Context, runs *bootstrap.Runs, id, fallback string) string {
	if r, err := runs.Get(ctx, id); err == nil {
		return r.State
	}
	return fallback
}
