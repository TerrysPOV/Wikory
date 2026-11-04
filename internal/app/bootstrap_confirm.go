package app

import (
	"context"
	"fmt"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
)

// bootstrapConfirm is the cost gate: the only way a run reaches sampling or running, and so the
// only way any paid model work starts. A person reads the plan, says yes, and the driver passes
// the plan hash back. The hash is advisory against a hostile local agent (it can read the hash
// from status), so this command also makes the honest paths safe: a changed store, a moved
// repository and a skipped sample are all refused.
func bootstrapConfirm(ctx context.Context, env *Env, args []string) (Result, error) {
	fl, err := parseFlags(normaliseBoolFlag(args, "sample"), map[string]bool{"run": true, "plan-hash": true, "sample": true})
	if err != nil {
		return Result{}, err
	}
	for _, v := range fl["sample"] {
		if v != "true" {
			return Result{}, &UsageError{Msg: "--sample takes no value: wikory bootstrap confirm --run RUN --plan-hash HASH --sample"}
		}
	}
	runID, hash := oneFlag(fl, "run"), oneFlag(fl, "plan-hash")
	if runID == "" || hash == "" {
		return Result{}, &UsageError{Msg: "usage: wikory bootstrap confirm --run RUN --plan-hash HASH [--sample]"}
	}
	p, runs, _, err := openRun(ctx, env, nil, map[string]bool{})
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	run, err := runs.Get(ctx, runID)
	if err != nil {
		return Result{}, err
	}
	// The hash the person approved is compared with one rebuilt from what the store holds, not
	// with the stored column: a store that no longer describes the plan that was shown is refused.
	rebuilt, err := rebuiltPlanHash(ctx, runs, run)
	if err != nil {
		return Result{}, err
	}
	if hash != rebuilt {
		return Result{}, &UsageError{Msg: "the plan hash does not match this run; read the plan again with wikory bootstrap status and pass its hash once the person has agreed to it"}
	}
	if rebuilt != run.PlanHash {
		return Result{}, &UsageError{Msg: "the stored run no longer matches the plan that was shown, so it cannot be confirmed; plan again"}
	}
	head, err := p.Repo.HeadCommit(ctx)
	if err != nil {
		return Result{}, err
	}
	if head != run.Head {
		return Result{}, &UsageError{Msg: fmt.Sprintf("the repository moved from %.12s to %.12s since this run was planned; the baseline describes the planned commit, so plan again", run.Head, head)}
	}
	sample := len(fl["sample"]) > 0
	switch {
	case sample && run.Sample == 0:
		return Result{}, &UsageError{Msg: "--sample needs a run planned with wikory bootstrap plan --sample N"}
	case sample:
		if err := runs.Transition(ctx, runID, bootstrap.StateSampling, bootstrap.StatePlanned); err != nil {
			return Result{}, err
		}
		if after, gerr := runs.Get(ctx, runID); gerr == nil && after.State == bootstrap.StateAwaiting {
			return Result{Text: fmt.Sprintf("Run %s had no sample items to run; it is awaiting confirmation of the full run.\n", runID),
				Data: map[string]any{"run_id": runID, "state": bootstrap.StateAwaiting}}, nil
		}
		return Result{Text: fmt.Sprintf("Sample of %d item(s) confirmed for run %s. Hand out work with wikory bootstrap next.\n", run.Sample, runID),
			Data: map[string]any{"run_id": runID, "state": bootstrap.StateSampling}}, nil
	case run.State == bootstrap.StatePlanned && run.Sample > 0:
		return Result{}, &UsageError{Msg: "this run was planned with a sample; confirm the sample first (wikory bootstrap confirm --sample), then confirm the full run once its measurements are in"}
	}
	// A run planned with a sample reaches the full run only through awaiting-confirm, which its
	// settled sample puts it in; the sample size is fixed at planning, so the source states are too.
	from := bootstrap.StatePlanned
	if run.Sample > 0 {
		from = bootstrap.StateAwaiting
	}
	if err := runs.Transition(ctx, runID, bootstrap.StateRunning, from); err != nil {
		if run.State == bootstrap.StateSampling {
			return Result{}, &UsageError{Msg: "the sample is still running; submit its items and the run will move to awaiting-confirm"}
		}
		return Result{}, err
	}
	return Result{Text: fmt.Sprintf("Run %s confirmed. Hand out work with wikory bootstrap next.\n", runID),
		Data: map[string]any{"run_id": runID, "state": bootstrap.StateRunning}}, nil
}

// rebuiltPlanHash rebuilds a run's plan hash through BuildPlan from the sources, components,
// scope, head and constants the store holds. The sample size is not part of the hash.
func rebuiltPlanHash(ctx context.Context, runs *bootstrap.Runs, run bootstrap.Run) (string, error) {
	srcs, err := runs.Sources(ctx, run.ID)
	if err != nil {
		return "", err
	}
	comps, err := runs.Components(ctx, run.ID)
	if err != nil {
		return "", err
	}
	return bootstrap.BuildPlan(bootstrap.Result{Sources: srcs, Components: comps}, run.Scope, run.Head, run.Sample).Hash, nil
}

// normaliseBoolFlag lets --NAME stand alone: it turns a bare --NAME into --NAME=true so the
// value-taking parser does not swallow the next argument.
func normaliseBoolFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--"+name {
			a = "--" + name + "=true"
		}
		out = append(out, a)
	}
	return out
}
