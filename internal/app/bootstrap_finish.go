package app

import (
	"context"
	"database/sql"
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

// maxNoticeIDs is how many claim ids a finish notice names before "and K more".
const maxNoticeIDs = 10

// buildCoverage is a test seam: a test makes it fail to show that a run whose report cannot be
// built is not marked done.
var buildCoverage = bootstrap.BuildCoverage

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
	if run.State == bootstrap.StateDone {
		// Finishing a done run again reprints the report stored when it was marked done and writes
		// nothing, so a finish killed after that move can be rerun to see its report.
		return storedFinishReport(ctx, runs, id)
	}
	if run.State != bootstrap.StateRunning && run.State != bootstrap.StateFinishing {
		return Result{}, fmt.Errorf("the run is %s; only a running or finishing run can be finished", run.State)
	}
	head, err := p.Repo.HeadCommit(ctx)
	if err != nil {
		return Result{}, err
	}
	if head != run.Head {
		if run.State == bootstrap.StateFinishing {
			// The run was already publishing when it stopped. It can never finish against another
			// commit, so name the way out instead of refusing for ever.
			return Result{}, &UsageError{Msg: fmt.Sprintf("the repository moved from %.12s to %.12s while this run was finishing, so it can never be finished; drop it with wikory bootstrap abandon --run %s, then plan again (pages it already wrote are still recognised as Wikory's own)", run.Head, head, run.ID)}
		}
		return Result{}, &UsageError{Msg: fmt.Sprintf("the repository moved from %.12s to %.12s since this run was planned; the baseline describes the planned commit, so plan again", run.Head, head)}
	}
	prog := newBootstrapProgress(env)
	// The progress line is ended on every way out of here, so an error never starts in the middle of it.
	staged := false
	beginStage := func(index, count int, name string, total int, expected time.Duration, notice string) {
		prog.BeginStage(index, count, name, total, expected, notice)
		staged = true
	}
	endStage := func() {
		if staged {
			staged = false
			prog.End()
		}
	}
	defer endStage()
	beginStage(1, 2, "prepare", 1, time.Second, "Checking and rendering the baseline pages. Safe to leave running; if it is interrupted, run finish again.")
	wf := bootstrap.WorkFiles{Root: p.Store.Root, CheckoutID: p.Checkout.ID}
	if n, err := bootstrap.PrepareVerify(ctx, runs, wf, id); err != nil {
		return Result{}, err
	} else if n > 0 {
		return Result{}, fmt.Errorf("%d verify item(s) were just created; run wikory bootstrap next to hand them out, submit the verdicts, then finish", n)
	}
	killPoint("prepared")
	if err := openItemsRefusal(ctx, runs, id); err != nil {
		return Result{}, err
	}
	killPoint("checked")
	pub := p.publisher()
	if _, err := pub.Recover(ctx); err != nil {
		return Result{}, err
	}
	entered, err := runs.EnterFinishing(ctx, id)
	if err != nil {
		return Result{}, err
	}
	// Nothing new can be handed out or committed from here, so this look is the authoritative one:
	// the checks above ran in separate transactions, and a submit or a next call can have landed
	// between any of them and the move. A refusal here undoes the move if this call made it, or
	// the run would sit in finishing with open work that nothing can close.
	if err := finishBlockers(ctx, runs, id); err != nil {
		if entered {
			if lerr := runs.LeaveFinishing(ctx, id); lerr != nil {
				err = errors.Join(err, fmt.Errorf("the run could not be put back to running: %w", lerr))
			}
		}
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
	endStage()
	current := func(path string) ([]byte, bool, error) {
		data, err := p.RepoRoot.ReadFile(path, 4<<20)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		if errors.Is(err, confine.ErrTooLarge) {
			// A page that has grown past the read limit is not ours: PlanOps must see content that
			// matches neither the new render nor anything recorded, and so file it as a conflict.
			return notOursContent, true, nil
		}
		return data, err == nil, err
	}
	var recErr error
	ours := func(path, hash string) bool {
		ok, err := runs.IsOurs(ctx, path, hash)
		if err != nil {
			recErr = err
		}
		return ok
	}
	ops, conflicts, unchanged, err := bootstrap.PlanOps(pages, current, ours)
	if err == nil {
		err = recErr
	}
	if err != nil {
		return Result{}, describeConfineErr(err)
	}
	killPoint("planned")
	outcomes := make([]bootstrap.PageOutcome, 0, len(pages))
	isConflict, isSame := map[string]bool{}, map[string]bool{}
	for _, c := range conflicts {
		isConflict[c] = true
	}
	for _, u := range unchanged {
		isSame[u] = true
	}
	written := map[string]string{}
	beginStage(2, 2, "publish", len(ops), time.Duration(len(ops))*100*time.Millisecond,
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
		// The pages this job will write are recorded as pending first, in one transaction: if the
		// process dies after the job and before the final record, they are still Wikory's own to
		// this run's rerun and to any later bootstrap.
		pending := make(map[string]string, end-start)
		for _, o := range ops[start:end] {
			pending[o.Target] = publish.HashContent(o.Content)
		}
		if err := runs.RecordPending(ctx, id, pending); err != nil {
			return Result{}, err
		}
		killPoint(fmt.Sprintf("pending-%d", jobNo))
		job, err := pub.Begin(ctx, "bootstrap")
		if err != nil {
			return Result{}, err
		}
		if err := pub.Publish(ctx, job, ops[start:end], fmt.Sprintf("bootstrap %s: %d page(s)", id, end-start)); err != nil {
			if errors.Is(err, publish.ErrConflict) {
				return Result{}, describePublishConflict(err, jobState(ctx, p.Store.DB, job.ID), jobNo)
			}
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
	endStage()
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
	// The report is built before the run is marked done and stored in the same transaction as
	// that move: a run that is done always has its report, and one whose report cannot be built
	// stays finishing, so finish can be run again.
	cov, err := buildCoverage(ctx, runs, id, outcomes, runs.Now())
	if err != nil {
		return Result{}, err
	}
	cov.Unindexed, cov.EndMarker = unindexed, marker
	if err := runs.Finish(ctx, id, marker, cov); err != nil {
		return Result{}, err
	}
	killPoint("marked-done")
	return Result{Data: cov, Text: cov.Text(), Notices: finishNotices(cov)}, nil
}

// storedFinishReport is what finish answers for a done run: the stored report, nothing written.
func storedFinishReport(ctx context.Context, runs *bootstrap.Runs, id string) (Result, error) {
	cov, ok, err := runs.StoredCoverage(ctx, id)
	if err != nil {
		return Result{}, err
	}
	if !ok {
		return Result{}, fmt.Errorf("the run is done but has no stored report (it finished before reports were stored)")
	}
	return Result{Data: cov, Text: cov.Text(), Notices: finishNotices(cov)}, nil
}

// finishNotices are the warnings that go with a coverage report. They are computed from the
// report alone, so a reprinted report carries the same ones.
func finishNotices(cov bootstrap.Coverage) []string {
	var notices []string
	if n := len(cov.Unindexed); n > 0 {
		notices = append(notices, fmt.Sprintf("%d claim(s) are on a published page but the index cannot read them, so search will not find them: %s", n, bootstrap.LabelList(cov.Unindexed, maxNoticeIDs)))
	}
	if len(cov.StalePages) > 0 {
		notices = append(notices, fmt.Sprintf("%d page(s) an earlier bootstrap wrote were not rendered this time and were left in place; the report lists them", len(cov.StalePages)))
	}
	conflicts := 0
	for _, pg := range cov.Pages {
		if pg.Outcome == "conflict" {
			conflicts++
		}
	}
	if conflicts > 0 {
		notices = append(notices, fmt.Sprintf("%d page(s) were edited by a person and were left alone; see the report", conflicts))
	}
	if cov.Components.Inspected > 0 && len(cov.Components.Verified)+len(cov.Components.Conflicting) == 0 {
		notices = append(notices, "no component has a verified or conflicting claim: the baseline is entirely unchecked against code")
	}
	return notices
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

// notOursContent stands for an existing page too large to read. It is not the new render and not
// anything a bootstrap recorded, so PlanOps files the page as a conflict.
var notOursContent = []byte("wikory: an existing page larger than the read limit; it is not ours\n")

// openItemsRefusal is the error for extract or verify work that is still pending or leased, or nil.
func openItemsRefusal(ctx context.Context, runs *bootstrap.Runs, id string) error {
	st, err := runs.Status(ctx, id)
	if err != nil {
		return err
	}
	for _, stage := range []string{"extract", "verify"} {
		if open := st.Items[stage]["pending"] + st.Items[stage]["leased"]; open > 0 {
			return fmt.Errorf("%d %s item(s) are still open; hand them out with wikory bootstrap next and submit them first", open, stage)
		}
	}
	return nil
}

// finishBlockers is the look finish takes once the run is finishing: no extract or verify item is
// open, and no verify work is owed that nothing has queued yet (a baseline must not be published
// unverified because the last extract submit landed after finish last looked).
func finishBlockers(ctx context.Context, runs *bootstrap.Runs, id string) error {
	if err := openItemsRefusal(ctx, runs, id); err != nil {
		return err
	}
	owed, err := bootstrap.VerifyOwed(ctx, runs, id)
	if err != nil {
		return err
	}
	if owed > 0 {
		return fmt.Errorf("%d component(s) have claims to verify but no verify item has been queued; run wikory bootstrap next to queue and hand them out, submit the verdicts, then finish", owed)
	}
	return nil
}

// finishConflict is a publish conflict with the explanation finish gives for it. It prints its
// own message, not the publisher's, whose sentence about earlier operations may not be true of
// this case, and keeps the cause for errors.Is.
type finishConflict struct {
	msg   string
	cause error
}

func (e *finishConflict) Error() string { return e.msg }
func (e *finishConflict) Unwrap() error { return e.cause }

// describePublishConflict explains a conflict from the publisher. jobState is the job's state in
// the store: "refused" means the publisher found the page changed before it applied anything;
// anything else, "conflict" included, means it was found while applying, when earlier operations
// of the same job may already be on disk. jobNo counts the publish jobs of this finish that
// completed before this one.
func describePublishConflict(err error, jobState string, jobNo int) error {
	const again = "Run wikory bootstrap finish again: it reads the pages afresh, reports a page a person created or edited as a conflict and leaves it alone."
	var msg string
	switch {
	case jobState == "refused" && jobNo == 0:
		msg = "a baseline page was created or edited after finish planned its writes; nothing was applied. " + again
	case jobState == "refused":
		msg = fmt.Sprintf("a baseline page was created or edited after finish planned its writes; nothing was applied from the job that found it, but the pages of the %d earlier job(s) of this finish are already written. %s", jobNo, again)
	default:
		msg = "a baseline page changed while finish was writing it, so earlier operations of this job may already be applied. " + again
	}
	return &finishConflict{msg: msg, cause: err}
}

// jobState reads a publish job's state, or "" when it cannot be read.
func jobState(ctx context.Context, db *sql.DB, jobID string) string {
	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM jobs WHERE job_id = ?`, jobID).Scan(&state); err != nil {
		return ""
	}
	return state
}
