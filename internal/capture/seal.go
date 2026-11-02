package capture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/TerrysPOV/Wikory/internal/decode/claude"
)

const maxSlice = 16 << 20

// SealError reports that Seal went on past turns it could not complete: Failed counts them
// and First is the first error. One bad record never blocks the turns behind it.
type SealError struct {
	Failed int
	First  error
}

func (e *SealError) Error() string {
	return fmt.Sprintf("capture: %d turn(s) could not be sealed; first error: %v", e.Failed, e.First)
}
func (e *SealError) Unwrap() error { return e.First }

// Seal completes captured records until deadline. It returns how many it sealed. A turn it
// cannot seal is skipped, not fatal: the error is a *SealError with the failure count and the
// first error, and the rest of the batch is still tried.
func (r *Recorder) Seal(ctx context.Context, deadline time.Time) (int, error) {
	rows, err := r.Store.DB.QueryContext(ctx, `SELECT turn_id, version FROM turns WHERE checkout_id = ? AND state = 'captured' ORDER BY created_at LIMIT 50`, r.CheckoutID)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id      string
		version int
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.version); err != nil {
			rows.Close()
			return 0, err
		}
		todo = append(todo, p)
	}
	rows.Close()
	sealed := 0
	var failed int
	var first error
	for _, p := range todo {
		if time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		ok, err := r.sealOne(ctx, p.id, p.version)
		if err != nil {
			failed++
			if first == nil {
				first = err
			}
			continue
		}
		if ok {
			sealed++
		}
	}
	if failed > 0 {
		return sealed, &SealError{Failed: failed, First: first}
	}
	return sealed, nil
}

// bound returns the part of the transcript that belongs to this turn.
//
// With identity: Claude Code writes promptId on user lines, equal to the hooks' prompt_id
// (V07), and the decoder carries it onto the lines that follow. When the slice holds lines
// with this turn's prompt id, exactly those lines are the turn, whatever the offsets say;
// this is what survives two turns starting before the first one's lines were flushed.
//
// Without identity: the turn is bounded at the start of the next turn in the same
// transcript (the next turn-start marker, by insertion order). If another turn of the same
// session and transcript recorded the same start offset, the boundary is ambiguous: the
// turn is never completed, and on timeout it is sealed incomplete with nothing attached.
func (r *Recorder) bound(ctx context.Context, rec Record, s claude.Slice) (claude.Slice, bool, error) {
	if rec.Marker != "" && hasPrompt(s, rec.Marker) {
		return s.ForPrompt(rec.Marker), true, nil
	}
	var same int
	if err := r.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM turn_starts ts1 JOIN turn_starts ts2
		ON ts2.session_id = ts1.session_id AND ts2.transcript_path = ts1.transcript_path
		AND ts2.transcript_offset = ts1.transcript_offset AND ts2.marker <> ts1.marker
		WHERE ts1.session_id = ? AND ts1.marker = ?`, rec.SessionID, rec.Marker).Scan(&same); err != nil {
		return s, false, err
	}
	if same > 0 {
		return claude.Slice{}, false, nil
	}
	var next int64
	err := r.Store.DB.QueryRowContext(ctx, `SELECT ts2.transcript_offset FROM turn_starts ts1
		JOIN turn_starts ts2 ON ts2.session_id = ts1.session_id AND ts2.transcript_path = ts1.transcript_path AND ts2.rowid > ts1.rowid
		WHERE ts1.session_id = ? AND ts1.marker = ? ORDER BY ts2.rowid LIMIT 1`, rec.SessionID, rec.Marker).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return s, true, nil
	}
	if err != nil {
		return s, false, err
	}
	return s.Before(next), true, nil
}

func hasPrompt(s claude.Slice, id string) bool {
	for _, m := range s.AssistantMessages {
		if m.PromptID == id {
			return true
		}
	}
	for _, u := range s.ToolUses {
		if u.PromptID == id {
			return true
		}
	}
	return false
}

func (r *Recorder) sealOne(ctx context.Context, id string, version int) (bool, error) {
	rec, err := r.readVersion(id, version)
	if err != nil {
		return false, err
	}
	captured, _ := time.Parse(time.RFC3339Nano, rec.CapturedAt)
	timedOut := r.Now().Sub(captured) >= r.SealTimeout
	slice, oversize, readErr := r.readSlice(rec.Transcript, maxSlice)
	end, complete := int64(0), false
	reason := ""
	switch {
	case errors.Is(readErr, errOutsideRoots):
		reason = ReasonOutsideRoot
	case readErr != nil:
		reason = ReasonUnreadable
	}
	if readErr == nil && rec.FinalMessageHash != "" {
		own, ok, err := r.bound(ctx, rec, slice)
		if err != nil {
			return false, err
		}
		if ok {
			slice = own
			end, complete = endMarker(slice, rec.FinalMessageHash)
		} else {
			reason = ReasonAmbiguous
		}
	}
	if !complete && !timedOut {
		return false, nil
	}
	next := rec
	next.State = "sealed"
	next.Incomplete = !complete
	if !complete {
		switch {
		case reason != "":
			next.IncompleteReason = reason
		case oversize:
			next.IncompleteReason = ReasonOversize
		default:
			next.IncompleteReason = ReasonNoFinal
		}
	}
	next.SealedAt = r.stamp()
	if complete {
		fillFromSlice(&next, slice, end)
	}
	published, ok, err := r.transition(ctx, id, version, next, func(state string, _ bool) bool { return state == "captured" })
	return ok && published.State == "sealed", err // an adopted captured version is not a seal
}

// readSlice reads at most limit bytes of the turn's slice. oversize is true when the
// transcript holds more than that, so a final message beyond the limit is never seen.
func (r *Recorder) readSlice(p Pointer, limit int64) (s claude.Slice, oversize bool, err error) {
	f, root, err := r.openPointer(p)
	if err != nil {
		return claude.Slice{}, false, err
	}
	defer root.Close()
	defer f.Close()
	if fi, serr := f.Stat(); serr == nil && fi.Size()-p.StartOffset > limit {
		oversize = true
	}
	s, err = claude.ReadSlice(f, p.StartOffset, limit)
	return s, oversize, err
}

// Why a sealed record is incomplete (Record.IncompleteReason).
const (
	ReasonOutsideRoot = "outside-root" // the transcript is not below an approved root
	ReasonUnreadable  = "unreadable"   // it could not be opened or read (missing, symlink, I/O)
	ReasonOversize    = "oversize"     // it holds more than the slice limit, so the end was not seen
	ReasonAmbiguous   = "ambiguous"    // two turns share a start offset with no prompt identity
	ReasonNoFinal     = "no-final"     // the final message never appeared before the seal timeout
)

// IncompleteSealed counts how many of the newest limit sealed turns of this checkout were
// sealed incomplete, and why. It reads at most limit small record files.
func (r *Recorder) IncompleteSealed(ctx context.Context, limit int) (int, map[string]int, error) {
	rows, err := r.Store.DB.QueryContext(ctx, `SELECT turn_id, version FROM turns WHERE checkout_id = ? AND state = 'sealed' ORDER BY updated_at DESC LIMIT ?`, r.CheckoutID, limit)
	if err != nil {
		return 0, nil, err
	}
	type row struct {
		id string
		v  int
	}
	var list []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.id, &x.v); err != nil {
			rows.Close()
			return 0, nil, err
		}
		list = append(list, x)
	}
	rows.Close()
	total, by := 0, map[string]int{}
	var first error
	for _, x := range list {
		rec, err := r.readVersion(x.id, x.v)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if rec.Incomplete {
			total++
			reason := rec.IncompleteReason
			if reason == "" {
				reason = "unspecified"
			}
			by[reason]++
		}
	}
	return total, by, first
}

// endMarker finds this turn's own final message: the first assistant message in the slice
// whose canonical hash matches. The turn ends there.
func endMarker(s claude.Slice, hash string) (int64, bool) {
	for _, m := range s.AssistantMessages {
		if finalHash(m.Text) == hash {
			return m.End, true
		}
	}
	return 0, false
}

// fillFromSlice adds only fields that are still empty, from lines before end.
func fillFromSlice(rec *Record, s claude.Slice, end int64) {
	rec.Transcript.EndOffset = end
	if len(rec.FilesTouched) == 0 {
		seen := map[string]bool{}
		for _, u := range s.ToolUses {
			if u.Offset < end && u.FilePath != "" && !seen[u.FilePath] {
				seen[u.FilePath] = true
				rec.FilesTouched = append(rec.FilesTouched, u.FilePath)
			}
		}
	}
	if len(rec.Commands) == 0 {
		for _, u := range s.ToolUses {
			if u.Offset >= end || u.Command == "" {
				continue
			}
			res, observed := s.Results[u.ID]
			rec.Commands = append(rec.Commands, Command{Text: u.Command, ToolUseID: u.ID, Observed: observed, Failed: observed && res.IsError})
			rec.Verification = append(rec.Verification, Verification{Command: u.Command, Observed: observed, Passed: observed && !res.IsError})
		}
	}
}
