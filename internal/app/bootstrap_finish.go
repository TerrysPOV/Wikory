package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/publish"
)

// maxOpsPerJob is a variable so a test can force several publish jobs from a small fixture.
var maxOpsPerJob = 40

const maxBytesPerJob = 2 << 20

// killPoint is a test seam like publish's kill hook: a crash test stops the process at a named
// step of finish, as a kill would.
var killPoint = func(string) {}

func bootstrapFinish(ctx context.Context, env *Env, args []string) (Result, error) {
	p, runs, fl, err := openRun(ctx, env, args, map[string]bool{"run": true})
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	id := oneFlag(fl, "run")
	if id == "" {
		return Result{}, &UsageError{Msg: "usage: wikory bootstrap finish --run RUN"}
	}
	run, err := runs.Get(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if run.State != bootstrap.StateRunning && run.State != bootstrap.StateFinishing {
		return Result{}, fmt.Errorf("the run is %s; only a running run can be finished", run.State)
	}
	head, err := p.Repo.HeadCommit(ctx)
	if err != nil {
		return Result{}, err
	}
	if head != run.Head {
		return Result{}, &UsageError{Msg: fmt.Sprintf("the repository moved from %.12s to %.12s since this run was planned; the baseline describes the planned commit, so plan again", run.Head, head)}
	}
	prog := newBootstrapProgress(env)
	prog.BeginStage(1, 2, "prepare", 1, time.Second, "Checking and rendering the baseline pages. Safe to leave running; if it is interrupted, run finish again.")
	wf := bootstrap.WorkFiles{Root: p.Store.Root, CheckoutID: p.Checkout.ID}
	if n, err := bootstrap.PrepareVerify(ctx, runs, wf, id); err != nil {
		return Result{}, err
	} else if n > 0 {
		return Result{}, fmt.Errorf("%d verify item(s) were just created; run wikory bootstrap next to hand them out, submit the verdicts, then finish", n)
	}
	st, err := runs.Status(ctx, id)
	if err != nil {
		return Result{}, err
	}
	for _, stage := range []string{"extract", "verify"} {
		if open := st.Items[stage]["pending"] + st.Items[stage]["leased"]; open > 0 {
			return Result{}, fmt.Errorf("%d %s item(s) are still open; hand them out with wikory bootstrap next and submit them first", open, stage)
		}
	}
	pub := p.publisher()
	if _, err := pub.Recover(ctx); err != nil {
		return Result{}, err
	}
	if err := runs.Transition(ctx, id, bootstrap.StateFinishing, bootstrap.StateRunning, bootstrap.StateFinishing); err != nil {
		return Result{}, err
	}
	comps, err := runs.Components(ctx, id)
	if err != nil {
		return Result{}, err
	}
	srcs, err := runs.Sources(ctx, id)
	if err != nil {
		return Result{}, err
	}
	claims, err := runs.Claims(ctx, id)
	if err != nil {
		return Result{}, err
	}
	pages := bootstrap.RenderPages(bootstrap.RenderModel{Run: run, Date: run.CreatedAt[:10], Components: comps, Sources: srcs, Claims: claims})
	// Nothing is written until every rendered page has been scanned: this slice, exactly as
	// RenderPages returned it, is what PlanOps and the publisher go on to use. The refusal names
	// the page and the kind of value, never the value, and a component page only by its hash
	// suffix (its file name carries a slug of a directory name from the project), so the refusal
	// cannot leak what it is refusing to publish.
	if bad := bootstrap.ScanPages(pages); len(bad) > 0 {
		return Result{}, refuseSecretPages(bad)
	}
	unindexed := bootstrap.Unrenderable(pages)
	prog.Step(1)
	prog.End()
	current := func(path string) ([]byte, bool, error) {
		data, err := p.RepoRoot.ReadFile(path, 4<<20)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return data, err == nil, err
	}
	var recErr error
	recorded := func(path string) (string, bool) {
		h, ok, err := runs.PublishedHash(ctx, path)
		if err != nil {
			recErr = err
		}
		return h, ok
	}
	ops, conflicts, unchanged, err := bootstrap.PlanOps(pages, current, recorded)
	if err == nil {
		err = recErr
	}
	if err != nil {
		return Result{}, describeConfineErr(err)
	}
	outcomes := make([]bootstrap.PageOutcome, 0, len(pages))
	isConflict, isSame := map[string]bool{}, map[string]bool{}
	for _, c := range conflicts {
		isConflict[c] = true
	}
	for _, u := range unchanged {
		isSame[u] = true
	}
	written := map[string]string{}
	prog.BeginStage(2, 2, "publish", len(ops), time.Duration(len(ops))*100*time.Millisecond,
		fmt.Sprintf("Publishing %d page(s). Safe to leave running; if it is interrupted, run finish again.", len(ops)))
	jobNo := 0
	for start := 0; start < len(ops); {
		end, bytes := start, 0
		for end < len(ops) && end-start < maxOpsPerJob && bytes+len(ops[end].Content) <= maxBytesPerJob {
			bytes += len(ops[end].Content)
			end++
		}
		if end == start {
			end++
		}
		job, err := pub.Begin(ctx, "bootstrap")
		if err != nil {
			return Result{}, err
		}
		if err := pub.Publish(ctx, job, ops[start:end], fmt.Sprintf("bootstrap %s: %d page(s)", id, end-start)); err != nil {
			return Result{}, err
		}
		for _, o := range ops[start:end] {
			written[o.Target] = publish.HashContent(o.Content)
		}
		start = end
		prog.Step(end)
		killPoint(fmt.Sprintf("after-job-%d", jobNo))
		jobNo++
	}
	prog.End()
	// A page that already equals the new render is our output too (for example after a killed
	// finish that recovery completed), so its hash is recorded and the next bootstrap will not
	// mistake it for a person's edit.
	content := map[string][]byte{}
	for _, pg := range pages {
		content[pg.Path] = pg.Content
	}
	for _, u := range unchanged {
		written[u] = publish.HashContent(content[u])
	}
	killPoint("before-record")
	if len(written) > 0 {
		if err := runs.RecordPublished(ctx, id, written); err != nil {
			return Result{}, err
		}
	}
	killPoint("recorded")
	for _, pg := range pages {
		o := "written"
		switch {
		case isConflict[pg.Path]:
			o = "conflict"
		case isSame[pg.Path]:
			o = "unchanged"
		}
		outcomes = append(outcomes, bootstrap.PageOutcome{Path: pg.Path, Outcome: o})
	}
	marker := fmt.Sprintf("%.12s %s", head, runs.Now().UTC().Format(time.RFC3339))
	if err := runs.Finish(ctx, id, marker); err != nil {
		return Result{}, err
	}
	killPoint("marked-done")
	cov, err := bootstrap.BuildCoverage(ctx, runs, id, outcomes, runs.Now())
	if err != nil {
		return Result{}, err
	}
	cov.Unindexed = unindexed
	var notices []string
	if len(unindexed) > 0 {
		notices = append(notices, fmt.Sprintf("%d claim(s) are on a published page but the index cannot read them, so search will not find them: %s", len(unindexed), strings.Join(unindexed, ", ")))
	}
	if len(cov.StalePages) > 0 {
		notices = append(notices, fmt.Sprintf("%d page(s) an earlier bootstrap wrote were not rendered this time and were left in place; the report lists them", len(cov.StalePages)))
	}
	if len(conflicts) > 0 {
		notices = append(notices, fmt.Sprintf("%d page(s) were edited by a person and were left alone; see the report", len(conflicts)))
	}
	if cov.Components.Inspected > 0 && len(cov.Components.Verified)+len(cov.Components.Conflicting) == 0 {
		notices = append(notices, "no component has a verified or conflicting claim: the baseline is entirely unchecked against code")
	}
	return Result{Data: cov, Text: cov.Text(), Notices: notices}, nil
}

// refuseSecretPages is the error for pages that would hold secret-shaped text. It names each
// page by PageLabel and the kinds of value found, never the text or a component's name.
func refuseSecretPages(bad []bootstrap.PageSecret) error {
	const shown = 5
	var names []string
	kinds := map[string]bool{}
	for i, b := range bad {
		if i < shown {
			names = append(names, bootstrap.PageLabel(b.Path))
		}
		for _, k := range b.Kinds {
			kinds[k] = true
		}
	}
	var ks []string
	for k := range kinds {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	list := strings.Join(names, ", ")
	if len(bad) > shown {
		list += fmt.Sprintf(" and %d more", len(bad)-shown)
	}
	return fmt.Errorf("page(s) %s would hold text that looks like a secret (%s); nothing was published. The text is not shown here: find it in the sources the page was built from, remove or redact it there, and plan again", list, strings.Join(ks, ", "))
}

// describeConfineErr wraps a confinement failure with the plain-language explanation.
func describeConfineErr(err error) error {
	if errors.Is(err, confine.ErrSymlink) || errors.Is(err, confine.ErrNotRegular) {
		return describeConfine(err)
	}
	return err
}
