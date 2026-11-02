package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/confine"
)

// Report lists what recovery did.
type Report struct {
	Completed []string
	Conflicts []string
	Refused   []string
	Abandoned []string
	Kept      []string   // backups recovery left beside a changed file, as repository-relative paths
	Errors    []JobError // jobs recovery could not process; the others were still processed
}

// JobError is a failure while recovering one job.
type JobError struct {
	JobID string
	Err   error
}

func (e JobError) Error() string { return "job " + e.JobID + ": " + e.Err.Error() }
func (e JobError) Unwrap() error { return e.Err }

// Recover finishes or reports every unfinished publish of this checkout. For each job it
// re-checks the tree fingerprint (a branch switch refuses the job, even when the target pages
// are identical), takes the lease over under the publishing lock (fencing the old holder),
// and then re-runs the protocol from the manifest, re-verifying operations already marked.
//
// One job that cannot be recovered does not stop the others: its failure goes into
// Report.Errors, naming the job, and Recover carries on. When any job failed, Recover also
// returns those failures joined, so a caller that only checks the error still sees them.
func (p *Publisher) Recover(ctx context.Context) (Report, error) {
	var rep Report
	if err := p.check(true); err != nil {
		return rep, err
	}
	unlock, err := p.lock()
	if err != nil {
		return rep, err
	}
	defer unlock()
	ents, err := p.Store.ReadDir("jobs")
	if err != nil {
		return rep, err
	}
	withManifest := map[string]bool{}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if err := p.recoverJob(ctx, e.Name(), withManifest, &rep); err != nil {
			rep.Errors = append(rep.Errors, JobError{JobID: e.Name(), Err: err})
		}
	}
	if err := p.abandonStale(ctx, withManifest, &rep); err != nil {
		return rep, err
	}
	failed := make([]error, len(rep.Errors))
	for i, e := range rep.Errors {
		failed[i] = e
	}
	return rep, errors.Join(failed...)
}

// finalState is a job state nothing moves a job out of.
func finalState(state string) bool {
	return state == "done" || state == "conflict" || state == "refused" || state == "abandoned"
}

// recoverJob recovers the job whose directory is name. A job directory without a manifest is
// left to abandonStale.
func (p *Publisher) recoverJob(ctx context.Context, name string, withManifest map[string]bool, rep *Report) error {
	data, err := p.Store.ReadFile(jobDir(name)+"/manifest.json", 1<<20)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	withManifest[name] = true // a manifest exists, readable or not, so the job is not "no manifest"
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("unreadable manifest: %w", err)
	}
	withManifest[m.JobID] = true
	if finalState(m.State) {
		return nil
	}
	if m.CheckoutID != p.CheckoutID {
		return nil // never applied to a different tree (spec 4.4)
	}
	var dbState string
	if err := p.DB.QueryRowContext(ctx, `SELECT state FROM jobs WHERE job_id = ?`, m.JobID).Scan(&dbState); err != nil {
		return err
	}
	if finalState(dbState) {
		// The database is authoritative. A manifest still saying applying is a record that could
		// not be brought up to date (or a crash between the two writes): skip the job and bring the
		// manifest in line, best effort, since the next recovery repeats it.
		m.State = dbState
		_ = p.writeManifest(&m)
		return nil
	}
	fp, err := p.Fingerprint(ctx)
	if err != nil {
		return err
	}
	if fp != m.Fingerprint {
		if err := p.setJob(ctx, m.JobID, "refused", "the tree changed (branch switch or new commit) before recovery"); err != nil {
			return err
		}
		m.State = "refused"
		_ = p.writeManifest(&m) // best effort: the database already says refused, and the next recovery repairs the manifest
		rep.Refused = append(rep.Refused, m.JobID)
		return nil
	}
	j, err := p.takeoverLocked(ctx, m.JobID)
	if err != nil {
		return err
	}
	m.Epoch = j.Epoch
	if err := p.writeManifest(&m); err != nil {
		return err
	}
	if err := p.recordOps(ctx, &m); err != nil {
		return err
	}
	kept, err := p.cleanTargets(&m)
	rep.Kept = append(rep.Kept, kept...)
	if err != nil {
		return err
	}
	if err := p.apply(ctx, &m); err != nil {
		if !errors.Is(err, ErrConflict) {
			return err
		}
		rep.Conflicts = append(rep.Conflicts, m.JobID)
		var rf recordFailure
		if errors.As(err, &rf) { // the conflict is reported, but it is not recorded as it should be
			rep.Errors = append(rep.Errors, JobError{JobID: m.JobID, Err: rf.err})
		}
		return nil
	}
	rep.Completed = append(rep.Completed, m.JobID)
	return nil
}

// abandonStale marks as abandoned every begun job of this checkout that has no manifest and
// is older than AbandonAfter: the process stopped before it wrote a manifest. It may have
// staged files by then (stageOps writes them before the manifest), so the stage directory is
// removed too.
func (p *Publisher) abandonStale(ctx context.Context, withManifest map[string]bool, rep *Report) error {
	rows, err := p.DB.QueryContext(ctx, `SELECT job_id, created_at FROM jobs WHERE checkout_id = ? AND state = 'begun'`, p.CheckoutID)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var id, created string
		if err := rows.Scan(&id, &created); err != nil {
			rows.Close()
			return err
		}
		if withManifest[id] {
			continue
		}
		t, perr := time.Parse(time.RFC3339Nano, created)
		if perr != nil {
			// An age that cannot be read is not an old age: the zero time would read as ancient and
			// abandon a job that may be live. Keep the job and say so.
			rep.Errors = append(rep.Errors, JobError{JobID: id, Err: fmt.Errorf("unreadable created_at %q, job kept: %w", created, perr)})
			continue
		}
		if p.Now().Sub(t) >= p.AbandonAfter {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if err := p.setJob(ctx, id, "abandoned", "no manifest written before the process stopped"); err != nil {
			return err
		}
		p.removeStage(id)
		rep.Abandoned = append(rep.Abandoned, id)
	}
	return nil
}

// removeStage deletes an abandoned job's stage directory and, once empty, its job directory.
// Best effort: leftover staged files are clutter, not state, and the job is already abandoned.
func (p *Publisher) removeStage(id string) {
	stage := jobDir(id) + "/stage"
	ents, err := p.Store.ReadDir(stage)
	if err != nil {
		return
	}
	for _, e := range ents {
		_ = p.Store.Remove(stage + "/" + e.Name())
	}
	_ = p.Store.Remove(stage)
	_ = p.Store.Remove(jobDir(id)) // fails harmlessly while anything is left in it
}

// cleanTargets undoes what an interrupted write left beside the targets: a backup whose
// target is missing (a kill after the move-aside) is put back, a backup whose target already
// holds the new content is removed, and temp files are deleted first (a kill after the install
// leaves the temp as a second hard link to the target, which confine refuses to read). Any other backup is kept
// beside a human file; its path is returned (and ends up in Report.Kept), and apply then raises the conflict.
func (p *Publisher) cleanTargets(m *manifest) ([]string, error) {
	dirs := map[string]bool{}
	newHash := map[string]string{}
	for _, op := range m.Ops {
		dirs[path.Dir(op.Target)] = true
		newHash[op.Target] = op.New
	}
	for d := range dirs {
		ents, err := p.Repo.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), confine.TempPrefix) {
				_ = p.Repo.Remove(d + "/" + e.Name())
			}
		}
	}
	var kept []string
	for d := range dirs {
		k, err := p.Repo.RestoreBackups(d, func(target string, cur []byte) bool {
			return HashContent(cur) == newHash[path.Join(d, target)]
		})
		for _, name := range k {
			kept = append(kept, path.Join(d, name))
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return kept, err
		}
	}
	return kept, nil
}
