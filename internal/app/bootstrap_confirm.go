package app

import (
	"context"
	"errors"
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
	// The stored run is checked first. The hash is rebuilt from what the store holds, and a run
	// whose store (or whose binary's constants) no longer reproduces the hash it was planned
	// under cannot be confirmed whatever hash the person passes. The remedy is a fresh plan: the
	// person's hash equals the stored column, and status prints that same column, so telling a
	// driver to read the hash again would send it round in a circle.
	rebuilt, err := rebuiltPlanHash(ctx, runs, run)
	if err != nil {
		return Result{}, err
	}
	if rebuilt != run.PlanHash {
		return Result{}, &UsageError{Msg: "the stored run no longer matches the plan that was shown (the store or the wikory binary changed since it was planned), so it cannot be confirmed; run wikory bootstrap plan again"}
	}
	if hash != run.PlanHash {
		return Result{}, &UsageError{Msg: "the plan hash given is not this plan's hash; the plan output prints it, and wikory bootstrap status --json prints it as run.plan_hash"}
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
	}
	// The one state check: a run planned with a sample reaches the full run only through
	// awaiting-confirm, which its settled sample puts it in, and nothing moves a run back to
	// planned. The sample size is fixed at planning, so the source state is too. The friendlier
	// messages are chosen after a refusal, from the state the run is in now.
	from := bootstrap.StatePlanned
	if run.Sample > 0 {
		from = bootstrap.StateAwaiting
	}
	if err := runs.Transition(ctx, runID, bootstrap.StateRunning, from); err != nil {
		if errors.Is(err, bootstrap.ErrState) {
			if now, gerr := runs.Get(ctx, runID); gerr == nil {
				switch {
				case now.State == bootstrap.StateSampling:
					return Result{}, &UsageError{Msg: "the sample is still running; submit its items and the run will move to awaiting-confirm"}
				case now.State == bootstrap.StatePlanned && now.Sample > 0:
					return Result{}, &UsageError{Msg: "this run was planned with a sample; confirm the sample first (wikory bootstrap confirm --sample), then confirm the full run once its measurements are in"}
				}
			}
		}
		return Result{}, err
	}
	return Result{Text: fmt.Sprintf("Run %s confirmed. Hand out work with wikory bootstrap next.\n", runID),
		Data: map[string]any{"run_id": runID, "state": bootstrap.StateRunning}}, nil
}

// rebuiltPlanHash rebuilds a run's plan hash through BuildPlan from the sources, components,
// scope and head the store holds, with the estimate constants the running binary has. The run's
// work items (bs_items) are not compared: the hash covers the work list derived from the
// sources, and the items table is advisory (architecture spec 14.4). The stored sample size is
// passed so the rebuilt plan is the one that was shown; BuildPlan does not hash it.
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
