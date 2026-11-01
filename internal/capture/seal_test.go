package capture

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/decode"
)

const (
	bashUse    = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`
	bashResult = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}`
	editUse    = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/repo/q.go"}}]}}`
	finalLine  = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done: all good."}]}}`

	// Lines for the tests of several turns in one transcript.
	useA     = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"a1","name":"Bash","input":{"command":"go test ./a"}}]}}`
	useB     = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"go test ./b"}}]}}`
	useC     = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"go test ./c"}}]}}`
	doneLine = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}`
)

func TestSealWaitsForTheFinalMessageThenFillsFacts(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "fix q", "p1", ""))
	r.appendTranscript(t, bashUse, bashResult, editUse) // lag: the final message is not written yet
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done: all good."))
	if n, err := r.rec.Seal(ctx, time.Now().Add(time.Second)); err != nil || n != 0 {
		t.Fatalf("sealed before the transcript held the final message: n=%d err=%v", n, err)
	}
	r.appendTranscript(t, finalLine)
	if n, err := r.rec.Seal(ctx, time.Now().Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	state, version, _, _ := r.row(t, id)
	body := r.readVersion(t, id, "v0002.json")
	if state != "sealed" || version != 2 || strings.Contains(body, `"incomplete": true`) ||
		!strings.Contains(body, `"/repo/q.go"`) || !strings.Contains(body, `"observed": true`) || !strings.Contains(body, `"passed": true`) {
		t.Fatalf("state=%s version=%d body=%s", state, version, body)
	}
	if strings.Contains(r.readVersion(t, id, "v0001.json"), "/repo/q.go") {
		t.Fatal("version 1 was rewritten")
	}
}

func TestSealTimeoutMarksIncomplete(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "never written to the transcript"))
	r.clk.t = r.clk.t.Add(11 * time.Minute) // past the 10 minute timeout
	if n, _ := r.rec.Seal(ctx, time.Now().Add(time.Second)); n != 1 {
		t.Fatalf("n=%d", n)
	}
	if !strings.Contains(r.readVersion(t, id, "v0002.json"), `"incomplete": true`) {
		t.Fatal("a timed-out seal must be marked incomplete")
	}
}

func TestSealedRecordNeverChanges(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	r.appendTranscript(t, finalLine)
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done: all good."))
	_, _ = r.rec.Seal(ctx, time.Now().Add(time.Second))
	_, _ = r.rec.Seal(ctx, time.Now().Add(time.Second))
	_, _, _ = r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done: all good."))
	if v := r.versions(t, id); len(v) != 2 {
		t.Fatalf("a sealed record changed: %v", v)
	}
}

func TestTranscriptOutsideApprovedRootsIsNeverRead(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.rec.TranscriptRoots = []string{t.TempDir()} // the real transcript is no longer under an approved root
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	r.appendTranscript(t, bashUse, bashResult, finalLine)
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done: all good."))
	if n, _ := r.rec.Seal(ctx, time.Now().Add(time.Second)); n != 0 {
		t.Fatal("sealed from a transcript outside the approved roots")
	}
	r.clk.t = r.clk.t.Add(11 * time.Minute)
	_, _ = r.rec.Seal(ctx, time.Now().Add(time.Second))
	if body := r.readVersion(t, id, "v0002.json"); strings.Contains(body, "go test") {
		t.Fatalf("transcript content was read: %s", body)
	}
}

func TestSessionStartStubsStaleMarkersOfOtherSessions(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "old work", "p-old", ""))
	r.clk.t = r.clk.t.Add(7 * time.Hour)
	if err := r.rec.SweepStale(ctx, "sess-2"); err != nil {
		t.Fatal(err)
	}
	if _, _, stub, outcome := r.row(t, TurnID("sess-1", "p-old")); !stub || outcome != "interrupted" {
		t.Fatalf("stub=%v outcome=%s", stub, outcome)
	}
}

func TestSweepLeavesRecentMarkersOfLiveSessions(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "in progress elsewhere", "p-live", ""))
	r.clk.t = r.clk.t.Add(5 * time.Minute)
	_ = r.rec.SweepStale(ctx, "sess-2")
	var n int
	_ = r.rec.Store.DB.QueryRow(`SELECT count(*) FROM turns`).Scan(&n)
	if n != 0 {
		t.Fatal("a live session's recent turn was stubbed")
	}
}
func TestUnobservedCommandIsNotVerified(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	r.appendTranscript(t, bashUse, finalLine) // the command's result never reached the transcript
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done: all good."))
	_, _ = r.rec.Seal(ctx, time.Now().Add(time.Second))
	body := r.readVersion(t, id, "v0002.json")
	if !strings.Contains(body, `"observed": false`) || strings.Contains(body, `"passed": true`) {
		t.Fatalf("an agent's claim is not an observation: %s", body)
	}
}

// Two turns end with the same text. Turn 1 is sealed after turn 2 has run: its slice must
// stop at turn 2's start, so it holds none of turn 2's commands.
func TestTurnSliceStopsAtTheNextTurn(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn one", "p1", ""))
	r.appendTranscript(t, useA, doneLine)
	id1, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done."))
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn two", "p2", ""))
	r.appendTranscript(t, useB, doneLine, useC) // useC follows turn 2's own end marker (a continuation)
	id2, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p2", "Done."))
	if n, err := r.rec.Seal(ctx, time.Now().Add(time.Second)); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	one, two := r.readVersion(t, id1, "v0002.json"), r.readVersion(t, id2, "v0002.json")
	if !strings.Contains(one, "go test ./a") || strings.Contains(one, "go test ./b") || !strings.Contains(two, "go test ./b") ||
		strings.Contains(two, "go test ./a") || strings.Contains(two, "go test ./c") {
		t.Fatalf("turn facts crossed a boundary:\nturn 1: %s\nturn 2: %s", one, two)
	}
}

// Turn 1's own final message never reached the transcript before turn 2 started; turn 2's
// identical "Done." must not complete turn 1.
func TestRepeatedFinalTextDoesNotCompleteTheWrongTurn(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn one", "p1", ""))
	id1, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done."))
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn two", "p2", ""))
	r.appendTranscript(t, useB, doneLine)
	_, _, _ = r.rec.Stop(ctx, r.ev(decode.Stop, "", "p2", "Done."))
	_, _ = r.rec.Seal(ctx, time.Now().Add(time.Second))
	if v := r.versions(t, id1); len(v) != 1 {
		t.Fatalf("turn 1 was completed from turn 2's lines: %v", v)
	}
}

// Every persisted text field is redacted, including commands copied from the transcript.
func TestSealedRecordRedactsCommandsAndVerification(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	bearer := "Bear" + "er " + strings.Repeat("Z", 32)
	aws := "AK" + "IA" + strings.Repeat("Q", 16)
	use := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"s1","name":"Bash","input":{"command":"curl -H 'Authorization: ` + bearer + `' https://x.invalid/` + aws + `"}}]}}`
	result := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"s1","is_error":false,"content":"ok"}]}}`
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "call the api", "p1", ""))
	r.appendTranscript(t, use, result, finalLine)
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done: all good."))
	_, _ = r.rec.Seal(ctx, time.Now().Add(time.Second))
	body := r.readVersion(t, id, "v0002.json")
	if strings.Contains(body, strings.Repeat("Z", 32)) || strings.Contains(body, strings.Repeat("Q", 16)) ||
		!strings.Contains(body, "[REDACTED:bearer]") || !strings.Contains(body, "[REDACTED:aws-access-key]") {
		t.Fatalf("a secret reached the store: %s", body)
	}
}

// A crashed earlier attempt left a captured version 2 on disk. A timed-out seal proposes
// version 2 as sealed, adopts the file instead, and must not count it as sealed.
func TestSealDoesNotCountAnAdoptedCapturedVersion(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "never written"))
	orphan, _ := r.rec.readVersion(id, 1)
	orphan.Version = 2
	if err := r.rec.writeVersion(&orphan); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(11 * time.Minute)
	n, err := r.rec.Seal(ctx, time.Now().Add(time.Second))
	state, version, _, _ := r.row(t, id)
	if err != nil || n != 0 || state != "captured" || version != 2 {
		t.Fatalf("an adopted captured version was counted as sealed: n=%d state=%s v%d err=%v", n, state, version, err)
	}
}

// Two turns start before turn 1's lines are flushed, so both record offset 0, and both
// end with the same text. With the transcript's prompt identity each turn gets exactly its
// own facts. Both records are inspected.
func TestDelayedFlushWithPromptIdentityAttributesEachTurn(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn one", "p1", ""))
	id1, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done."))
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn two", "p2", "")) // still offset 0
	r.appendTranscript(t,
		`{"type":"user","promptId":"p1","message":{"role":"user","content":"turn one"}}`,
		useA,
		doneLine,
		`{"type":"user","promptId":"p2","message":{"role":"user","content":"turn two"}}`,
		useB,
		doneLine)
	id2, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p2", "Done."))
	if n, err := r.rec.Seal(ctx, time.Now().Add(time.Second)); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	one, two := r.readVersion(t, id1, "v0002.json"), r.readVersion(t, id2, "v0002.json")
	if !strings.Contains(one, "go test ./a") || strings.Contains(one, "go test ./b") ||
		!strings.Contains(two, "go test ./b") || strings.Contains(two, "go test ./a") {
		t.Fatalf("turn facts crossed a boundary:\nturn 1: %s\nturn 2: %s", one, two)
	}
}

// The same delayed flush without prompt identity: the boundary is ambiguous, so neither
// turn is completed, and on timeout both are sealed incomplete with nothing attached.
func TestDelayedFlushWithoutIdentityAttachesNothing(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn one", "p1", ""))
	id1, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done."))
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn two", "p2", ""))
	r.appendTranscript(t,
		useA,
		doneLine,
		useB,
		doneLine)
	id2, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p2", "Done."))
	if n, _ := r.rec.Seal(ctx, time.Now().Add(time.Second)); n != 0 {
		t.Fatalf("an ambiguous turn was completed: n=%d", n)
	}
	r.clk.t = r.clk.t.Add(11 * time.Minute)
	if n, err := r.rec.Seal(ctx, time.Now().Add(time.Second)); err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, id := range []string{id1, id2} {
		body := r.readVersion(t, id, "v0002.json")
		if !strings.Contains(body, `"incomplete": true`) || strings.Contains(body, "go test") {
			t.Fatalf("an ambiguous turn was given facts: %s", body)
		}
	}
}

// Turn 1's final message never reached the transcript, and turn 2 started at a later
// offset and ended with the same text. The next-turn bound alone must keep turn 2's lines
// from completing turn 1.
func TestRepeatedFinalTextAcrossDistinctOffsetsDoesNotCompleteTheWrongTurn(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn one", "p1", ""))
	r.appendTranscript(t, useA) // turn 1's own final message is never written
	id1, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done."))
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "turn two", "p2", "")) // a later offset
	r.appendTranscript(t, useB, doneLine)
	_, _, _ = r.rec.Stop(ctx, r.ev(decode.Stop, "", "p2", "Done."))
	_, _ = r.rec.Seal(ctx, time.Now().Add(time.Second))
	if v := r.versions(t, id1); len(v) != 1 {
		t.Fatalf("turn 1 was completed from turn 2's lines: %v", v)
	}
}
