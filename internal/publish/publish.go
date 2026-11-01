// Package publish writes several repository files, the publish log and the database as one
// recoverable operation (spec 6, publish protocol).
package publish

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/store"
)

// Absent is the base hash of a file that must not exist yet.
const Absent = "absent"

const maxFile = 4 << 20

var (
	// ErrConflict: a target no longer holds what the job expected. A conflict found while
	// applying leaves any earlier operations of the job applied; a human's edit is never
	// overwritten.
	ErrConflict      = errors.New("publish: a target changed since the job's base; earlier operations of this job may already be applied; the job is in state conflict")
	ErrStaleEpoch    = errors.New("publish: lease epoch is stale; another worker took over this job")
	ErrFingerprint   = errors.New("publish: the checkout's tree changed while the job was open")
	ErrWrongCheckout = errors.New("publish: the job belongs to another checkout")
	ErrJobState      = errors.New("publish: the job is not open")
	// ErrDuplicateTarget: two operations of one job name the same target, so the second
	// could only ever conflict with the first.
	ErrDuplicateTarget = errors.New("publish: two operations name the same target")
	// ErrNotConfigured: the Publisher is missing a field it needs.
	ErrNotConfigured = errors.New("publish: the Publisher is not fully configured")
)

// logScanChunk is how much of the log is read at a time when searching it for a job's line.
const logScanChunk = 64 << 10

// maxLogScan bounds how much of an oversize log is searched for a job's line.
const maxLogScan = 64 << 20

// Test hooks. No-ops in production.
var (
	killHook            = func(string) {} // crash tests stop the process at a named step
	testHookBeforeApply = func(int) {}    // runs after an op's first hash check, before its write
)

// HashContent identifies file content in manifests and base hashes.
func HashContent(b []byte) string { return ids.SHA256Hex(b) }

// Op is one file operation.
type Op struct {
	Target  string
	OldHash string
	Content []byte
	Perm    fs.FileMode
}

// Job is an open publish job held at one lease epoch.
type Job struct {
	ID          string
	CheckoutID  string
	Kind        string
	Fingerprint string
	Epoch       int64
}

// Publisher publishes into one checkout.
type Publisher struct {
	DB           *sql.DB
	Store        *confine.Root
	Repo         *confine.Root
	CheckoutID   string
	LockPath     string
	LogPath      string
	Fingerprint  func(context.Context) (string, error)
	AbandonAfter time.Duration
	Now          func() time.Time
}

type manifestOp struct {
	Target string `json:"target"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Staged string `json:"staged"`
	Perm   uint32 `json:"perm"`
	State  string `json:"state"`
}

type manifest struct {
	Schema      string       `json:"schema"`
	JobID       string       `json:"job_id"`
	CheckoutID  string       `json:"checkout_id"`
	Fingerprint string       `json:"fingerprint"`
	Epoch       int64        `json:"epoch"`
	State       string       `json:"state"`
	LogLine     string       `json:"log_line"`
	Ops         []manifestOp `json:"ops"`
}

func (p *Publisher) now() string { return p.Now().UTC().Format(time.RFC3339Nano) }

func jobDir(id string) string { return "jobs/" + id }

func (p *Publisher) lock() (func(), error) { return store.Lock(p.LockPath, 10*time.Second) }

// check reports a Publisher that lacks a field it needs, so a zero value fails with a clear
// error instead of a nil-function panic (or, for AbandonAfter 0, by abandoning live jobs).
// It runs lazily at each entry point, so struct literals that set every field keep working.
func (p *Publisher) check(forRecovery bool) error {
	var missing []string
	add := func(bad bool, name string) {
		if bad {
			missing = append(missing, name)
		}
	}
	add(p.DB == nil, "DB")
	add(p.Store == nil, "Store")
	add(p.Repo == nil, "Repo")
	add(p.CheckoutID == "", "CheckoutID")
	add(p.LockPath == "", "LockPath")
	add(p.LogPath == "", "LogPath")
	add(p.Fingerprint == nil, "Fingerprint")
	add(p.Now == nil, "Now")
	add(forRecovery && p.AbandonAfter <= 0, "AbandonAfter (must be positive)")
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrNotConfigured, strings.Join(missing, ", "))
	}
	return nil
}

// Begin opens a job at epoch 1 and captures the tree fingerprint.
func (p *Publisher) Begin(ctx context.Context, kind string) (Job, error) {
	if err := p.check(false); err != nil {
		return Job{}, err
	}
	fp, err := p.Fingerprint(ctx)
	if err != nil {
		return Job{}, err
	}
	j := Job{ID: ids.JobID(), CheckoutID: p.CheckoutID, Kind: kind, Fingerprint: fp, Epoch: 1}
	_, err = p.DB.ExecContext(ctx, `INSERT INTO jobs(job_id, checkout_id, kind, state, epoch, fingerprint, created_at, updated_at)
		VALUES(?, ?, ?, 'begun', 1, ?, ?, ?)`, j.ID, j.CheckoutID, kind, fp, p.now(), p.now())
	return j, err
}

// Takeover raises the job's lease epoch, fencing whoever held the old one. It runs under
// the publishing lock, so it can never land between two steps of a publish in progress.
func (p *Publisher) Takeover(ctx context.Context, jobID string) (Job, error) {
	if err := p.check(false); err != nil {
		return Job{}, err
	}
	unlock, err := p.lock()
	if err != nil {
		return Job{}, err
	}
	defer unlock()
	return p.takeoverLocked(ctx, jobID)
}

func (p *Publisher) takeoverLocked(ctx context.Context, jobID string) (Job, error) {
	res, err := p.DB.ExecContext(ctx, `UPDATE jobs SET epoch = epoch + 1, updated_at = ? WHERE job_id = ? AND checkout_id = ? AND state IN ('begun','applying')`, p.now(), jobID, p.CheckoutID)
	if err != nil {
		return Job{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		var owner string
		if qerr := p.DB.QueryRowContext(ctx, `SELECT checkout_id FROM jobs WHERE job_id = ?`, jobID).Scan(&owner); qerr == nil && owner != p.CheckoutID {
			return Job{}, ErrWrongCheckout // never fence another checkout's job
		}
		return Job{}, ErrJobState
	}
	var j Job
	err = p.DB.QueryRowContext(ctx, `SELECT job_id, checkout_id, kind, fingerprint, epoch FROM jobs WHERE job_id = ?`, jobID).
		Scan(&j.ID, &j.CheckoutID, &j.Kind, &j.Fingerprint, &j.Epoch)
	return j, err
}

// fence refuses to continue unless the job is still open at this epoch. apply calls it before
// each operation and before the closing steps, so a holder whose lease was taken over stops
// at its next one. The database writes in between (recordOps, markDone, the final state
// update) carry the epoch in their SQL instead and report ErrStaleEpoch when it no longer
// matches.
func (p *Publisher) fence(ctx context.Context, jobID string, epoch int64) error {
	var state string
	var cur int64
	if err := p.DB.QueryRowContext(ctx, `SELECT state, epoch FROM jobs WHERE job_id = ?`, jobID).Scan(&state, &cur); err != nil {
		return err
	}
	if cur != epoch {
		return ErrStaleEpoch
	}
	if state != "begun" && state != "applying" {
		return ErrJobState
	}
	return nil
}

func (p *Publisher) setJob(ctx context.Context, id, state, detail string) error {
	_, err := p.DB.ExecContext(ctx, `UPDATE jobs SET state = ?, detail = ?, updated_at = ? WHERE job_id = ?`, state, detail, p.now(), id)
	return err
}

// currentHash is the target's content hash, Absent when missing. A symlink or other
// non-regular target is a conflict, never followed.
func (p *Publisher) currentHash(target string) (string, error) {
	data, err := p.Repo.ReadFile(target, maxFile)
	switch {
	case err == nil:
		return HashContent(data), nil
	case errors.Is(err, fs.ErrNotExist):
		return Absent, nil
	case errors.Is(err, confine.ErrSymlink) || errors.Is(err, confine.ErrNotRegular) || errors.Is(err, confine.ErrNotDir):
		return "", fmt.Errorf("%w: %s is not a regular file", ErrConflict, target)
	default:
		return "", err
	}
}

// Publish runs the protocol for one job.
func (p *Publisher) Publish(ctx context.Context, j Job, ops []Op, logLine string) error {
	if err := p.check(false); err != nil {
		return err
	}
	unlock, err := p.lock()
	if err != nil {
		return err
	}
	defer unlock()
	killHook("locked")
	if err := p.revalidate(ctx, j, ops); err != nil {
		return err
	}
	m, err := p.stageOps(j, ops, logLine)
	if err != nil {
		return err
	}
	killHook("staged")
	if err := p.writeManifest(m); err != nil {
		return err
	}
	if err := p.recordOps(ctx, m); err != nil {
		return err
	}
	killHook("manifest")
	return p.apply(ctx, m)
}

// revalidate is step 1 of the protocol: fencing and revalidation. The job must still be
// begun at this epoch for this checkout, the tree fingerprint must be unchanged, and every
// target must still hold its base content. A refusal that is final is recorded on the job.
func (p *Publisher) revalidate(ctx context.Context, j Job, ops []Op) error {
	var state, checkout string
	var epoch int64
	if err := p.DB.QueryRowContext(ctx, `SELECT state, checkout_id, epoch FROM jobs WHERE job_id = ?`, j.ID).Scan(&state, &checkout, &epoch); err != nil {
		return err
	}
	if state != "begun" {
		return ErrJobState
	}
	if epoch != j.Epoch {
		return ErrStaleEpoch
	}
	if checkout != p.CheckoutID || j.CheckoutID != p.CheckoutID {
		return ErrWrongCheckout
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if seen[op.Target] {
			return fmt.Errorf("%w: %s", ErrDuplicateTarget, op.Target)
		}
		seen[op.Target] = true
	}
	fp, err := p.Fingerprint(ctx)
	if err != nil {
		return err
	}
	if fp != j.Fingerprint {
		_ = p.setJob(ctx, j.ID, "refused", "fingerprint changed")
		return ErrFingerprint
	}
	for _, op := range ops {
		cur, err := p.currentHash(op.Target)
		if err != nil {
			_ = p.setJob(ctx, j.ID, "refused", err.Error())
			return err
		}
		if cur != op.OldHash {
			_ = p.setJob(ctx, j.ID, "refused", "base hash mismatch: "+op.Target)
			return fmt.Errorf("%w: %s", ErrConflict, op.Target)
		}
	}
	return nil
}

// stageOps is the staging half of step 2: it writes every new version into the job's stage
// directory and returns the manifest that describes them.
func (p *Publisher) stageOps(j Job, ops []Op, logLine string) (*manifest, error) {
	if err := p.Store.MkdirAll(jobDir(j.ID)+"/stage", 0o700); err != nil {
		return nil, err
	}
	m := &manifest{Schema: "wikory.manifest/v1", JobID: j.ID, CheckoutID: j.CheckoutID, Fingerprint: j.Fingerprint,
		Epoch: j.Epoch, State: "applying", LogLine: logLine}
	for i, op := range ops {
		staged := fmt.Sprintf("%s/stage/%d", jobDir(j.ID), i)
		if err := p.Store.WriteFileAtomic(staged, op.Content, 0o600); err != nil {
			return nil, err
		}
		perm := op.Perm
		if perm == 0 {
			perm = 0o644
		}
		m.Ops = append(m.Ops, manifestOp{Target: op.Target, Old: op.OldHash, New: HashContent(op.Content), Staged: staged, Perm: uint32(perm), State: "pending"})
	}
	return m, nil
}

// fencedOneRow turns an epoch-guarded update that matched no row into ErrStaleEpoch.
func fencedOneRow(res sql.Result) error {
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrStaleEpoch
	}
	return nil
}

// recordOps marks the job applying and records each operation as pending, in one transaction.
func (p *Publisher) recordOps(ctx context.Context, m *manifest) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'applying', updated_at = ? WHERE job_id = ? AND epoch = ?`, p.now(), m.JobID, m.Epoch)
	if err != nil {
		return err
	}
	if err := fencedOneRow(res); err != nil {
		return err
	}
	for i, op := range m.Ops {
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_ops(job_id, idx, target, old_hash, new_hash, state) VALUES(?, ?, ?, ?, ?, 'pending')
			ON CONFLICT(job_id, idx) DO NOTHING`, m.JobID, i, op.Target, op.Old, op.New); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *Publisher) writeManifest(m *manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return p.Store.WriteFileAtomic(jobDir(m.JobID)+"/manifest.json", data, 0o600)
}

// markDone records an applied operation in the database, fenced by the epoch.
func (p *Publisher) markDone(ctx context.Context, m *manifest, i int) error {
	res, err := p.DB.ExecContext(ctx, `UPDATE job_ops SET state = 'done' WHERE job_id = ? AND idx = ?
		AND EXISTS (SELECT 1 FROM jobs WHERE job_id = ? AND epoch = ? AND state = 'applying')`, m.JobID, i, m.JobID, m.Epoch)
	if err != nil {
		return err
	}
	return fencedOneRow(res)
}

// apply runs steps 3 to 5 from whatever state the manifest records. Recovery calls it too.
// Operations already marked done are re-verified on disk, never trusted.
func (p *Publisher) apply(ctx context.Context, m *manifest) error {
	for i := range m.Ops {
		op := &m.Ops[i]
		if err := p.fence(ctx, m.JobID, m.Epoch); err != nil {
			return err
		}
		cur, err := p.currentHash(op.Target)
		if err != nil {
			return p.conflict(ctx, m, err.Error())
		}
		if op.State == "done" {
			if cur != op.New {
				return p.conflict(ctx, m, op.Target+" changed after Wikory applied it")
			}
			if err := p.markDone(ctx, m, i); err != nil { // a kill may have come between the two marks
				return err
			}
			continue
		}
		switch {
		case cur == op.New:
			// Applied before a crash; only the marks are missing.
		case cur == op.Old:
			data, err := p.Store.ReadFile(op.Staged, maxFile)
			if err != nil {
				return err
			}
			if HashContent(data) != op.New {
				return fmt.Errorf("publish: staged content for %s is corrupt", op.Target)
			}
			if dir := path.Dir(op.Target); dir != "." {
				if err := p.Repo.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			testHookBeforeApply(i)
			if err := p.write(op, data); err != nil {
				if errors.Is(err, confine.ErrChanged) || errors.Is(err, confine.ErrExist) {
					return p.conflict(ctx, m, op.Target+": "+err.Error())
				}
				return err
			}
			killHook(fmt.Sprintf("applied-%d", i))
		default:
			return p.conflict(ctx, m, "unexpected content in "+op.Target)
		}
		op.State = "done"
		if err := p.writeManifest(m); err != nil {
			return err
		}
		killHook(fmt.Sprintf("marked-%d", i))
		if err := p.markDone(ctx, m, i); err != nil {
			return err
		}
		killHook(fmt.Sprintf("dbmarked-%d", i))
	}
	if err := p.fence(ctx, m.JobID, m.Epoch); err != nil {
		return err
	}
	if m.State == "applying" {
		if err := p.appendLogOnce(m); err != nil {
			return err
		}
		killHook("appended")
		m.State = "logged"
		if err := p.writeManifest(m); err != nil {
			return err
		}
	}
	killHook("logged")
	res, err := p.DB.ExecContext(ctx, `UPDATE jobs SET state = 'done', updated_at = ? WHERE job_id = ? AND epoch = ? AND state = 'applying'`, p.now(), m.JobID, m.Epoch)
	if err != nil {
		return err
	}
	if err := fencedOneRow(res); err != nil {
		return err
	}
	killHook("committed")
	m.State = "done"
	return p.writeManifest(m)
}

// write applies one operation without clobbering: a new file is created exclusively, and an
// existing one goes through confine's move-aside replace, which judges the moved-aside
// content against the base hash and installs with a no-clobber link, so a human edit made
// before the move-aside, or recreating the name afterwards, is detected and kept (spec 4.4).
// The publish lock excludes every other Wikory writer. The residual: an editor that already
// holds the original file open and writes through that descriptor after the compare writes
// to the moved inode, which is then unlinked, and that edit is lost (see
// confine.ReplaceIfUnchanged).
func (p *Publisher) write(op *manifestOp, data []byte) error {
	if op.Old == Absent {
		return p.Repo.CreateExclusive(op.Target, data, fs.FileMode(op.Perm))
	}
	return p.Repo.ReplaceIfUnchanged(op.Target, data, fs.FileMode(op.Perm), func(current []byte) bool {
		return HashContent(current) == op.Old
	})
}

// recordFailure marks the part of a conflict error that says the conflict itself could not
// be recorded, so Recover can report it beside the conflict.
type recordFailure struct{ err error }

func (r recordFailure) Error() string { return r.err.Error() }
func (r recordFailure) Unwrap() error { return r.err }

// conflict records the conflict in the database (authoritative) and the manifest, and returns
// ErrConflict. A failure to record either is returned with it, never discarded: the job could
// otherwise read applying in one place and conflict in the other.
func (p *Publisher) conflict(ctx context.Context, m *manifest, detail string) error {
	err := fmt.Errorf("%w: %s", ErrConflict, detail)
	m.State = "conflict"
	var failed []error
	if serr := p.setJob(ctx, m.JobID, "conflict", detail); serr != nil {
		failed = append(failed, fmt.Errorf("recording the conflict in the database: %w", serr))
	}
	if merr := p.writeManifest(m); merr != nil {
		failed = append(failed, fmt.Errorf("recording the conflict in the manifest: %w", merr))
	}
	if len(failed) > 0 {
		return errors.Join(err, recordFailure{errors.Join(failed...)})
	}
	return err
}

// rotatedLogName is the name a full log is moved to, in its own directory: log.md becomes
// log.1.md. One rotated log is kept; the next rotation replaces it.
func rotatedLogName(logPath string) string {
	base := path.Base(logPath)
	ext := path.Ext(base)
	return strings.TrimSuffix(base, ext) + ".1" + ext
}

// scanLog reports the log's size and whether it already holds token, reading it in chunks
// (never the whole file) and at most maxLogScan bytes. A missing log is empty.
func (p *Publisher) scanLog(token []byte) (size int64, found bool, err error) {
	f, err := p.Repo.OpenRead(p.LogPath)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	buf := make([]byte, logScanChunk+len(token))
	carry := 0 // the tail of the previous chunk, so a token straddling two chunks is still found
	for read := int64(0); read < maxLogScan; {
		n, rerr := f.Read(buf[carry : carry+logScanChunk])
		if bytes.Contains(buf[:carry+n], token) {
			return fi.Size(), true, nil
		}
		read += int64(n)
		data := buf[:carry+n]
		carry = min(len(token)-1, len(data))
		copy(buf, data[len(data)-carry:])
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return 0, false, rerr
		}
	}
	return fi.Size(), false, nil
}

// appendLogOnce adds one log.md line for the job unless one is already there. A log that the
// line would push past the cap is rotated to log.1.md first, so a full log never turns an
// applied publish into a permanent error.
func (p *Publisher) appendLogOnce(m *manifest) error {
	size, found, err := p.scanLog([]byte("job=" + m.JobID))
	if err != nil || found {
		return err
	}
	line := fmt.Sprintf("- %s job=%s %s\n", p.Now().UTC().Format(time.RFC3339), m.JobID, m.LogLine)
	if size+int64(len(line)) > maxFile {
		if err := p.Repo.Rename(p.LogPath, rotatedLogName(p.LogPath)); err != nil {
			return err
		}
	}
	return p.Repo.AppendFile(p.LogPath, []byte(line), 0o644)
}
