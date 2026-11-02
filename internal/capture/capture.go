package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/decode"
	"github.com/TerrysPOV/Wikory/internal/gitx"
	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/redact"
	"github.com/TerrysPOV/Wikory/internal/store"
)

const (
	maxRequest = 2000
	maxFinal   = 4000
	maxCommand = 2000
	maxPath    = 1000
	maxDirty   = 50
)

// testHookBeforeTransition lets a test hold two transitions at a barrier. Nil in production.
var testHookBeforeTransition func(turnID string)

// UnknownRevision is recorded as GitFacts.Head when HEAD could not be resolved.
const UnknownRevision = "unknown"

// Recorder captures turns for one registered checkout.
type Recorder struct {
	Store            *store.Store
	CheckoutID       string
	Repo             *gitx.Repo
	RepoRoot         *confine.Root
	TranscriptRoots  []string
	Now              func() time.Time
	SealTimeout      time.Duration
	StaleMarkerAfter time.Duration
	// Warn, when set, is told the kind of a fault the capture survived (for example
	// "git-status" when the dirty-file facts were lost), so the caller can log it.
	Warn func(kind string)
}

// tsFormat is fixed width so stored timestamps compare correctly as strings.
const tsFormat = "2006-01-02T15:04:05.000000000Z"

func (r *Recorder) stamp() string { return r.Now().UTC().Format(tsFormat) }

const truncMark = " [truncated]"

// capText truncates to n runes. It is idempotent: already-capped text is left alone.
func capText(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n || (strings.HasSuffix(s, truncMark) && len(rs) <= n+len([]rune(truncMark))) {
		return s
	}
	return string(rs[:n]) + truncMark
}

// clean is the only way text reaches the store: redact first, then truncate, so a secret
// can never be split by truncation into a shape the redactor no longer recognises.
func clean(s string, max int, counts map[string]int) string {
	out, c := redact.String(s)
	for k, v := range c {
		counts[k] += v
	}
	return capText(out, max)
}

// redactRecord cleans every persisted text field of rec in place.
func redactRecord(rec *Record) {
	if rec.Redactions == nil {
		rec.Redactions = map[string]int{}
	}
	c := rec.Redactions
	rec.Request = clean(rec.Request, maxRequest, c)
	rec.FinalMessage = clean(rec.FinalMessage, maxFinal, c)
	rec.Transcript.Rel = clean(rec.Transcript.Rel, maxPath, c)
	rec.Git.Branch = clean(rec.Git.Branch, maxPath, c)
	for i := range rec.FilesTouched {
		rec.FilesTouched[i] = clean(rec.FilesTouched[i], maxPath, c)
	}
	for i := range rec.Commands {
		rec.Commands[i].Text = clean(rec.Commands[i].Text, maxCommand, c)
	}
	for i := range rec.Verification {
		rec.Verification[i].Command = clean(rec.Verification[i].Command, maxCommand, c)
	}
	for i := range rec.Git.Dirty {
		rec.Git.Dirty[i].Path = clean(rec.Git.Dirty[i].Path, maxPath, c)
	}
	if len(c) == 0 {
		rec.Redactions = nil
	}
}

func finalHash(s string) string { return ids.SHA256Hex([]byte(claim.CanonicalText(s))) }

func isMemoryAgent(ev decode.Event) bool { return strings.HasPrefix(ev.AgentType, "wikory-") }

// TurnStart records a durable turn-start marker (spec 6) and stubs earlier turns of this
// session that never reached a Stop.
func (r *Recorder) TurnStart(ctx context.Context, ev decode.Event) error {
	marker := ev.PromptID
	if marker == "" {
		marker = "ups:" + ids.Digest(ev.SessionID, r.stamp())
	}
	if err := r.stubOpen(ctx, `ts.session_id = ? AND ts.marker <> ?`, ev.SessionID, marker); err != nil {
		return err
	}
	req := clean(ev.Prompt, maxRequest, map[string]int{})
	ptr, _ := r.locate(ev.TranscriptPath)
	size, at := r.transcriptSize(ev.TranscriptPath), r.stamp()
	return r.Store.WithWriteLock(ctx, func(ctx context.Context) error {
		_, err := r.Store.DB.ExecContext(ctx, `INSERT INTO turn_starts(session_id, marker, checkout_id, transcript_path, transcript_offset, request, at)
		VALUES(?, ?, ?, ?, ?, ?, ?) ON CONFLICT(session_id, marker) DO NOTHING`,
			ev.SessionID, marker, r.CheckoutID, encodePointer(ptr), size, req, at)
		return err
	})
}

// SweepStale stubs open markers of other sessions in this checkout older than StaleMarkerAfter.
func (r *Recorder) SweepStale(ctx context.Context, currentSession string) error {
	cutoff := r.Now().Add(-r.StaleMarkerAfter).UTC().Format(tsFormat)
	return r.stubOpen(ctx, `ts.session_id <> ? AND ts.at < ?`, currentSession, cutoff)
}

type turnStart struct {
	session, marker, path, request string
	offset                         int64
}

// stubOpen writes an interrupted stub for each open marker matching cond. cond is one of
// two fixed strings in this file; it never contains caller input.
func (r *Recorder) stubOpen(ctx context.Context, cond string, args ...any) error {
	q := `SELECT ts.session_id, ts.marker, ts.transcript_path, ts.transcript_offset, ts.request FROM turn_starts ts
		WHERE ts.checkout_id = ? AND ` + cond + ` AND NOT EXISTS (SELECT 1 FROM turns t WHERE t.session_id = ts.session_id AND t.marker = ts.marker)`
	rows, err := r.Store.DB.QueryContext(ctx, q, append([]any{r.CheckoutID}, args...)...)
	if err != nil {
		return err
	}
	var open []turnStart
	for rows.Next() {
		var s turnStart
		if err := rows.Scan(&s.session, &s.marker, &s.path, &s.offset, &s.request); err != nil {
			rows.Close()
			return err
		}
		open = append(open, s)
	}
	rows.Close()
	for _, s := range open {
		rec := Record{Schema: RecordSchema, TurnID: TurnID(s.session, s.marker), Version: 1, State: "captured",
			Outcome: "interrupted", Stub: true, Harness: "claude-code", SessionID: s.session, Marker: s.marker,
			CheckoutID: r.CheckoutID, Request: s.request, Transcript: withOffset(decodePointer(s.path), s.offset),
			SummaryOrigin: "deterministic", CapturedAt: r.stamp()}
		if _, _, err := r.createOrComplete(ctx, rec); err != nil {
			return err
		}
	}
	return nil
}

func (r *Recorder) lookupStart(ctx context.Context, session, marker string) (turnStart, bool, error) {
	s := turnStart{session: session}
	var row *sql.Row
	if marker != "" {
		row = r.Store.DB.QueryRowContext(ctx, `SELECT marker, transcript_path, transcript_offset, request FROM turn_starts
			WHERE session_id = ? AND marker = ?`, session, marker)
	} else {
		row = r.Store.DB.QueryRowContext(ctx, `SELECT marker, transcript_path, transcript_offset, request FROM turn_starts
			WHERE session_id = ? ORDER BY at DESC LIMIT 1`, session)
	}
	err := row.Scan(&s.marker, &s.path, &s.offset, &s.request)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	return s, err == nil, err
}

// newRecord builds version 1 of a turn from a Stop event and its marker. Text is cleaned
// when the record is written, never here.
func (r *Recorder) newRecord(ev decode.Event, marker string) Record {
	return Record{Schema: RecordSchema, TurnID: TurnID(ev.SessionID, marker), Version: 1, State: "captured",
		Outcome: "completed", Harness: ev.Harness, SessionID: ev.SessionID, Marker: marker, CheckoutID: r.CheckoutID,
		FinalMessage: ev.LastAssistantMessage, FinalMessageHash: finalHash(ev.LastAssistantMessage),
		SummaryOrigin: "deterministic", CapturedAt: r.stamp()}
}

func withOffset(p Pointer, off int64) Pointer {
	p.StartOffset = off
	return p
}

// Stop captures the turn deterministically. created is false for a duplicate.
func (r *Recorder) Stop(ctx context.Context, ev decode.Event) (string, bool, error) {
	if isMemoryAgent(ev) {
		return "", false, nil
	}
	start, found, err := r.lookupStart(ctx, ev.SessionID, ev.PromptID)
	if err != nil {
		return "", false, err
	}
	marker := ev.PromptID
	if marker == "" && found {
		marker = start.marker
	}
	if marker == "" {
		marker = "stop:" + ids.Digest(ev.TranscriptPath, fmt.Sprint(r.transcriptSize(ev.TranscriptPath)))
	}
	rec := r.newRecord(ev, marker)
	rec.Transcript, _ = r.locate(ev.TranscriptPath)
	if found {
		rec.Request, rec.Transcript.StartOffset = start.request, start.offset
		if start.path != "" {
			rec.Transcript = withOffset(decodePointer(start.path), start.offset)
		}
	}
	rec.Git = r.gitFacts(ctx)
	return r.createOrComplete(ctx, rec)
}

func (r *Recorder) turnDir(turnID string) string {
	return fmt.Sprintf("turns/%s/%s", r.CheckoutID, turnID)
}

func (r *Recorder) versionPath(turnID string, v int) string {
	return fmt.Sprintf("%s/v%04d.json", r.turnDir(turnID), v)
}

// writeVersion cleans every text field, then creates one immutable version file.
// ErrExist means that version is already on disk.
func (r *Recorder) writeVersion(rec *Record) error {
	redactRecord(rec)
	if err := r.Store.Root.MkdirAll(r.turnDir(rec.TurnID), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return r.Store.Root.CreateExclusive(r.versionPath(rec.TurnID, rec.Version), data, 0o600)
}

func (r *Recorder) readVersion(turnID string, v int) (Record, error) {
	var rec Record
	data, err := r.Store.Root.ReadFile(r.versionPath(turnID, v), 1<<22)
	if err != nil {
		return rec, err
	}
	return rec, json.Unmarshal(data, &rec)
}

// insertRow inserts the turn's row, inside the database write lock.
func (r *Recorder) insertRow(ctx context.Context, rec Record) error {
	created, updated := r.stamp(), r.stamp()
	return r.Store.WithWriteLock(ctx, func(ctx context.Context) error {
		_, err := r.Store.DB.ExecContext(ctx, `INSERT INTO turns(turn_id, checkout_id, session_id, marker, state, version, outcome, stub, request, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(turn_id) DO NOTHING`,
			rec.TurnID, rec.CheckoutID, rec.SessionID, rec.Marker, rec.State, rec.Version, rec.Outcome, boolInt(rec.Stub), rec.Request, created, updated)
		return err
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// transition moves a turn from version from to the next version as one serialised step.
// Inside one immediate transaction (the database write lock) it re-reads the row and yields
// if the version moved or allowed refuses the state; it then writes the immutable file for
// version from+1, adopting that file if a crashed earlier attempt already wrote it, and
// publishes the row from the content of the file actually on disk. The row therefore never
// passes the file on disk (a file may briefly run ahead of the row after a crash, and the
// next transition adopts it), and a transition can never rewind a turn to an older version.
// It returns the record actually published, which is the adopted file when one was on
// disk, so a caller never counts a proposal that did not take effect.
func (r *Recorder) transition(ctx context.Context, turnID string, from int, next Record, allowed func(state string, stub bool) bool) (Record, bool, error) {
	if testHookBeforeTransition != nil {
		testHookBeforeTransition(turnID)
	}
	var (
		published Record
		ok        bool
	)
	// The whole transaction runs inside the cross-process database write lock (spec 4.5): on
	// Windows SQLite's own locking loses writes between processes.
	err := r.Store.WithWriteLock(ctx, func(ctx context.Context) (err error) {
		published, ok, err = r.transitionLocked(ctx, turnID, from, next, allowed)
		return err
	})
	return published, ok, err
}

// transitionLocked is the body of transition. The caller holds the database write lock.
func (r *Recorder) transitionLocked(ctx context.Context, turnID string, from int, next Record, allowed func(state string, stub bool) bool) (Record, bool, error) {
	tx, err := r.Store.DB.BeginTx(ctx, nil) // _txlock=immediate: the SQLite write lock is taken here
	if err != nil {
		return Record{}, false, err
	}
	defer tx.Rollback()
	var version, stub int
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT version, state, stub FROM turns WHERE turn_id = ?`, turnID).Scan(&version, &state, &stub); err != nil {
		return Record{}, false, err
	}
	if version != from || !allowed(state, stub == 1) {
		return Record{}, false, nil // another transition won; this one yields
	}
	next.Version = from + 1
	if err := r.writeVersion(&next); errors.Is(err, confine.ErrExist) {
		adopted, rerr := r.readVersion(turnID, next.Version)
		if rerr != nil {
			return Record{}, false, rerr
		}
		next = adopted
	} else if err != nil {
		return Record{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE turns SET version = ?, state = ?, stub = ?, outcome = ?, updated_at = ? WHERE turn_id = ?`,
		next.Version, next.State, boolInt(next.Stub), next.Outcome, r.stamp(), turnID); err != nil {
		return Record{}, false, err
	}
	return next, true, tx.Commit()
}

// createOrComplete is create-if-absent, plus the single permitted change to an existing
// record through a Stop: an interrupted stub, still captured, is completed once by the late
// Stop for the same turn. It uses the same serialised transition as sealing; a sealed record,
// stub or not, never changes.
func (r *Recorder) createOrComplete(ctx context.Context, rec Record) (string, bool, error) {
	err := r.writeVersion(&rec)
	if err == nil {
		return rec.TurnID, true, r.insertRow(ctx, rec)
	}
	if !errors.Is(err, confine.ErrExist) {
		return "", false, err
	}
	var version, stub int
	var state string
	qerr := r.Store.DB.QueryRowContext(ctx, `SELECT version, state, stub FROM turns WHERE turn_id = ?`, rec.TurnID).Scan(&version, &state, &stub)
	if errors.Is(qerr, sql.ErrNoRows) {
		first, err := r.readVersion(rec.TurnID, 1) // crash between file and row: repair from the file
		if err != nil {
			return "", false, err
		}
		if err := r.insertRow(ctx, first); err != nil {
			return "", false, err
		}
		// The repaired row may be an interrupted stub that this Stop completes: continue
		// into the completion path below instead of returning with the stub in place.
		version, state, stub = first.Version, first.State, boolInt(first.Stub)
	} else if qerr != nil {
		return "", false, qerr
	}
	if stub == 1 && !rec.Stub && state == "captured" {
		next := rec
		next.Stub, next.Outcome, next.State = false, "completed", "captured"
		if _, _, err := r.transition(ctx, rec.TurnID, version, next, func(s string, isStub bool) bool { return s == "captured" && isStub }); err != nil {
			return "", false, err
		}
	}
	return rec.TurnID, false, nil
}

// gitFacts records branch, HEAD and fingerprints of uncommitted files, read through the
// confined repository root. git is given at most half of what is left of ctx, so a git that
// blocks (a FIFO as .git/info/exclude, a hung filesystem) loses the dirty-file facts but not
// the turn; Warn("git-status") says so.
func (r *Recorder) gitFacts(ctx context.Context) GitFacts {
	gctx := ctx
	if dl, ok := ctx.Deadline(); ok {
		if half := time.Until(dl) / 2; half > 0 {
			var cancel context.CancelFunc
			gctx, cancel = context.WithTimeout(ctx, half)
			defer cancel()
		}
	}
	g := GitFacts{Branch: r.Repo.Branch(gctx)}
	head, err := r.Repo.HeadCommit(gctx)
	if err != nil {
		// "" means an unborn branch and nothing else; a failure is recorded as unknown.
		head = UnknownRevision
		if r.Warn != nil {
			r.Warn("git")
		}
	}
	g.Head = head
	dirty, err := r.Repo.Dirty(gctx, ".")
	if err != nil {
		if r.Warn != nil {
			r.Warn("git-status")
		}
		return g
	}
	for i, p := range dirty {
		if i == maxDirty {
			break
		}
		data, err := r.RepoRoot.ReadFile(p, 1<<20)
		if err != nil {
			g.Dirty = append(g.Dirty, Fingerprint{Path: p})
			continue
		}
		g.Dirty = append(g.Dirty, Fingerprint{Path: p, SHA256: ids.SHA256Hex(data), Size: int64(len(data))})
	}
	return g
}
