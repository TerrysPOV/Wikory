package capture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/decode"
)

func TestDuplicateStopNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if err := r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "do A", "p1", "")); err != nil {
		t.Fatal(err)
	}
	id, created, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "FIRST answer"))
	if err != nil || !created || id != TurnID("sess-1", "p1") {
		t.Fatalf("%s %v %v", id, created, err)
	}
	_, created, err = r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "SECOND answer"))
	if err != nil || created {
		t.Fatalf("duplicate Stop created=%v err=%v", created, err)
	}
	if v := r.versions(t, id); len(v) != 1 {
		t.Fatalf("versions %v", v)
	}
	body := r.readVersion(t, id, "v0001.json")
	if !strings.Contains(body, "FIRST answer") || strings.Contains(body, "SECOND") || !strings.Contains(body, `"request": "do A"`) {
		t.Fatalf("record: %s", body)
	}
	if state, version, _, _ := r.row(t, id); state != "captured" || version != 1 {
		t.Fatalf("row %s %d", state, version)
	}
}

func TestStopWithoutPromptIDUsesStoredMarker(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "do B", "", ""))
	id1, created, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "", "answer B"))
	if err != nil || !created {
		t.Fatal(created, err)
	}
	id2, created, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "", "answer B again"))
	if id2 != id1 || created {
		t.Fatalf("a repeated Stop for the same turn must map to the same record: %s %s %v", id1, id2, created)
	}
	if !strings.Contains(r.readVersion(t, id1, "v0001.json"), `"request": "do B"`) {
		t.Fatal("request from the stored marker missing")
	}
}

func TestInterruptionBeforeFirstStop(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "do A", "p1", ""))
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "do B", "p2", "")) // p1 never got a Stop
	stub := TurnID("sess-1", "p1")
	if state, version, isStub, outcome := r.row(t, stub); state != "captured" || version != 1 || !isStub || outcome != "interrupted" {
		t.Fatalf("stub row %s %d %v %s", state, version, isStub, outcome)
	}
	if _, created, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p2", "B done")); !created {
		t.Fatal("p2 should be captured normally")
	}
	// A late Stop for p1 completes the stub once, as a new version.
	if _, created, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "A finished late")); err != nil || created {
		t.Fatalf("late stop: created=%v err=%v", created, err)
	}
	if _, version, isStub, outcome := r.row(t, stub); version != 2 || isStub || outcome != "completed" {
		t.Fatalf("upgraded row %d %v %s", version, isStub, outcome)
	}
	if !strings.Contains(r.readVersion(t, stub, "v0001.json"), `"stub": true`) {
		t.Fatal("version 1 must be unchanged")
	}
	if _, _, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "third")); err != nil || len(r.versions(t, stub)) != 2 {
		t.Fatalf("a completed stub must not be upgraded again: %v %v", err, r.versions(t, stub))
	}
}

func TestMemorySubagentStopIsExcluded(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	ev := r.ev(decode.Stop, "", "p9", "summary")
	ev.AgentType = "wikory-recorder"
	if id, created, err := r.rec.Stop(ctx, ev); err != nil || created || id != "" {
		t.Fatalf("%s %v %v", id, created, err)
	}
}

func TestRequestAndFinalAreRedacted(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	secret := "gh" + "p_" + strings.Repeat("Z", 36)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "use "+secret, "p1", ""))
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "set it to "+secret))
	body := r.readVersion(t, id, "v0001.json")
	if strings.Contains(body, strings.Repeat("Z", 36)) || !strings.Contains(body, "[REDACTED:github-token]") {
		t.Fatalf("secret reached the store: %s", body)
	}
	var stored string
	_ = r.rec.Store.DB.QueryRow(`SELECT request FROM turn_starts WHERE marker = 'p1'`).Scan(&stored)
	if strings.Contains(stored, strings.Repeat("Z", 36)) {
		t.Fatalf("the turn-start row stored the raw request: %q", stored)
	}
}

// A transition from a version that is no longer current must yield, never rewind the turn.
func TestStaleTransitionNeverRewindsVersion(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "y"))
	rec, _ := r.rec.readVersion(id, 1)
	next := rec
	next.State = "captured"
	if _, ok, err := r.rec.transition(ctx, id, 1, next, func(string, bool) bool { return true }); !ok || err != nil {
		t.Fatalf("first transition: %v %v", ok, err)
	}
	if _, ok, err := r.rec.transition(ctx, id, 2, next, func(string, bool) bool { return true }); !ok || err != nil {
		t.Fatalf("second transition: %v %v", ok, err)
	}
	stale := rec
	stale.State = "sealed"
	if _, ok, err := r.rec.transition(ctx, id, 1, stale, func(string, bool) bool { return true }); ok || err != nil {
		t.Fatalf("a transition from version 1 must yield once the turn is at version 3: ok=%v err=%v", ok, err)
	}
	if _, version, _, _ := r.row(t, id); version != 3 {
		t.Fatalf("the turn was rewound to version %d", version)
	}
}

// A crash after a version file was written but before the row moved: the next transition
// adopts the file on disk, so the row always says what the file says.
func TestOrphanedVersionFileIsAdopted(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "y"))
	rec, _ := r.rec.readVersion(id, 1)
	orphan := rec
	orphan.Version, orphan.State, orphan.Outcome = 2, "captured", "orphan-from-a-crash"
	if err := r.rec.writeVersion(&orphan); err != nil {
		t.Fatal(err)
	}
	proposed := rec
	proposed.State = "sealed"
	if _, ok, err := r.rec.transition(ctx, id, 1, proposed, func(string, bool) bool { return true }); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if state, version, _, outcome := r.row(t, id); state != "captured" || version != 2 || outcome != "orphan-from-a-crash" {
		t.Fatalf("the row disagrees with the file on disk: %s %d %s", state, version, outcome)
	}
}

func TestCrashBetweenFileAndRowIsRepaired(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	rec := r.rec.newRecord(r.ev(decode.Stop, "", "p1", "y"), "p1")
	if err := r.rec.writeVersion(&rec); err != nil { // the file exists, the row does not
		t.Fatal(err)
	}
	if _, created, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "y")); err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if state, version, _, _ := r.row(t, rec.TurnID); state != "captured" || version != 1 {
		t.Fatalf("row not repaired: %s %d", state, version)
	}
}

// A crash after an interrupted stub's file was written but before its row: the next Stop
// for that turn repairs the row from the file and then completes the stub, once.
func TestRepairedOrphanStubIsCompletedByItsStop(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	stub := Record{Schema: RecordSchema, TurnID: TurnID("sess-1", "p1"), Version: 1, State: "captured", Outcome: "interrupted",
		Stub: true, Harness: "claude-code", SessionID: "sess-1", Marker: "p1", CheckoutID: r.rec.CheckoutID,
		SummaryOrigin: "deterministic", CapturedAt: r.rec.stamp()}
	if err := r.rec.writeVersion(&stub); err != nil { // the stub file exists, its row does not
		t.Fatal(err)
	}
	if _, _, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "late answer")); err != nil {
		t.Fatal(err)
	}
	state, version, isStub, outcome := r.row(t, stub.TurnID)
	if state != "captured" || version != 2 || isStub || outcome != "completed" || len(r.versions(t, stub.TurnID)) != 2 {
		t.Fatalf("the repaired stub was not completed: %s v%d stub=%v %s %v", state, version, isStub, outcome, r.versions(t, stub.TurnID))
	}
}

// Every persisted string is redacted, metadata included: a token-shaped branch name and
// file name never reach the store, and no absolute transcript path is persisted.
func TestMetadataIsRedactedAndNoAbsolutePathIsStored(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	tok := "ghp_" + strings.Repeat("A1b2", 9)
	for _, args := range [][]string{{"checkout", "-q", "-b", "fix-" + tok}} {
		if out, err := exec.Command("git", append([]string{"-C", r.repoDir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(r.repoDir, "notes-"+tok+".txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	id, _, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "y"))
	if err != nil {
		t.Fatal(err)
	}
	body := r.readVersion(t, id, "v0001.json")
	var stored string
	_ = r.rec.Store.DB.QueryRow(`SELECT transcript_path FROM turn_starts WHERE marker = 'p1'`).Scan(&stored)
	if strings.Contains(body, tok) || strings.Contains(stored, tok) || !strings.Contains(body, `"branch": "fix-`) {
		t.Fatalf("a token-shaped branch or file name was persisted:\n%s", body)
	}
	if strings.Contains(body, r.transcript) || strings.Contains(stored, r.transcript) || filepath.IsAbs(stored) || !strings.Contains(body, `"rel": "session.jsonl"`) {
		t.Fatalf("an absolute transcript path was persisted: %q\n%s", stored, body)
	}
}
