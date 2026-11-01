package publish

import (
	"context"
	"encoding/json"
	"errors"
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
}

// Recover finishes or reports every unfinished publish of this checkout. For each job it
// re-checks the tree fingerprint (a branch switch refuses the job, even when the target pages
// are identical), takes the lease over under the publishing lock (fencing the old holder),
// and then re-runs the protocol from the manifest, re-verifying operations already marked.
func (p *Publisher) Recover(ctx context.Context) (Report, error) {
	var rep Report
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
		data, err := p.Store.ReadFile(jobDir(e.Name())+"/manifest.json", 1<<20)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return rep, err
		}
		var m manifest
		if err := json.Unmarshal(data, &m); err != nil {
			return rep, err
		}
		withManifest[m.JobID] = true
		if m.State == "done" || m.State == "conflict" || m.State == "refused" {
			continue
		}
		if m.CheckoutID != p.CheckoutID {
			continue // never applied to a different tree (spec 4.4)
		}
		var dbState string
		if err := p.DB.QueryRowContext(ctx, `SELECT state FROM jobs WHERE job_id = ?`, m.JobID).Scan(&dbState); err != nil {
			return rep, err
		}
		if dbState == "done" {
			m.State = "done"
			if err := p.writeManifest(&m); err != nil {
				return rep, err
			}
			continue
		}
		fp, err := p.Fingerprint(ctx)
		if err != nil {
			return rep, err
		}
		if fp != m.Fingerprint {
			m.State = "refused"
			_ = p.writeManifest(&m)
			if err := p.setJob(ctx, m.JobID, "refused", "the tree changed (branch switch or new commit) before recovery"); err != nil {
				return rep, err
			}
			rep.Refused = append(rep.Refused, m.JobID)
			continue
		}
		j, err := p.takeoverLocked(ctx, m.JobID)
		if err != nil {
			return rep, err
		}
		m.Epoch = j.Epoch
		if err := p.writeManifest(&m); err != nil {
			return rep, err
		}
		if err := p.recordOps(ctx, &m); err != nil {
			return rep, err
		}
		if err := p.cleanTargets(&m); err != nil {
			return rep, err
		}
		if err := p.apply(ctx, &m); err != nil {
			if errors.Is(err, ErrConflict) {
				rep.Conflicts = append(rep.Conflicts, m.JobID)
				continue
			}
			return rep, err
		}
		rep.Completed = append(rep.Completed, m.JobID)
	}
	if err := p.abandonStale(ctx, withManifest, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// abandonStale marks as abandoned every begun job of this checkout that has no manifest and
// is older than AbandonAfter: the process stopped before it staged anything.
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
		t, _ := time.Parse(time.RFC3339Nano, created)
		if !withManifest[id] && p.Now().Sub(t) >= p.AbandonAfter {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if err := p.setJob(ctx, id, "abandoned", "no manifest written before the process stopped"); err != nil {
			return err
		}
		rep.Abandoned = append(rep.Abandoned, id)
	}
	return nil
}

// cleanTargets undoes what an interrupted write left beside the targets: a backup whose
// target is missing (a kill after the move-aside) is put back, a backup whose target already
// holds the new content is removed, and temp files are deleted first (a kill after the install
// leaves the temp as a second hard link to the target, which confine refuses to read). Any other backup is kept
// beside a human file and reported by the conflict that apply then raises.
func (p *Publisher) cleanTargets(m *manifest) error {
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
	for d := range dirs {
		if _, err := p.Repo.RestoreBackups(d, func(target string, cur []byte) bool {
			return HashContent(cur) == newHash[path.Join(d, target)]
		}); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
