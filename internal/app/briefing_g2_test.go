package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/capture"
)

func logLine(at time.Time, kind string) string {
	return at.UTC().Format(time.RFC3339) + " " + kind + "\n"
}

// Doctor counts the last 24 hours, so a failure from last week no longer counts and one from
// this morning does. It raises a notice, and the briefing's status line says so.
func TestHookErrorsInTheLastDayReachDoctorAndTheBriefing(t *testing.T) {
	h := newHookRig(t)
	now := time.Now()
	log := logLine(now.Add(-30*time.Hour), "stop") + logLine(now.Add(-2*time.Hour), "capture") + logLine(now.Add(-time.Minute), "timeout")
	if err := os.WriteFile(filepath.Join(h.c.dataDir, "hook-errors.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	e := doctorFull(t, h.c, h.dir)
	if e.Data["hook_errors"] != float64(2) || !anyContains(e.Notices, "hook error") {
		t.Fatalf("hook_errors=%v notices=%v", e.Data["hook_errors"], e.Notices)
	}
	if got := h.context(t, h.dir); !strings.Contains(got, "hook errors 2") {
		t.Fatalf("the briefing does not say hooks have been failing: %s", got)
	}
	if out, _, _ := h.c.run(h.dir, "context"); !strings.Contains(out, "hook errors 2") {
		t.Fatalf("wikory context does not say hooks have been failing: %s", out)
	}
}

func TestBriefingSaysNothingAboutHookErrorsWhenThereAreNone(t *testing.T) {
	h := newHookRig(t)
	if got := h.context(t, h.dir); strings.Contains(got, "hook errors") {
		t.Fatalf("%s", got)
	}
}

// The log is capped: past 1 MiB only the newest 256 KiB are kept, and the newest line survives.
func TestHookErrorLogIsCapped(t *testing.T) {
	h := newHookRig(t)
	path := filepath.Join(h.c.dataDir, "hook-errors.log")
	var b strings.Builder
	old := time.Now().Add(-3 * time.Hour)
	for b.Len() < 2<<20 {
		b.WriteString(logLine(old, "capture"))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	h.hook(t, h.dir, "no-such-event", h.payload("Stop", h.dir, nil)) // logs unknown-event
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	kinds := h.logKinds(t)
	if fi.Size() > hookLogKeep+4096 || len(kinds) == 0 || kinds[len(kinds)-1] != "unknown-event" {
		t.Fatalf("size=%d kinds=%d last=%v: the log was not capped to its newest part", fi.Size(), len(kinds), kinds[len(kinds)-1:])
	}
	if strings.HasPrefix(h.logText(t), "capture") || !strings.HasPrefix(h.logText(t), "20") {
		t.Fatalf("the trimmed log starts mid-line: %.40q", h.logText(t))
	}
}

func TestPendingCountIsNotCappedAtTheRowLimit(t *testing.T) {
	h := newHookRig(t)
	db, co := h.storeDB(t), h.checkoutID(t)
	base := time.Now().Add(-2 * time.Hour)
	for i := 0; i < 60; i++ {
		at := base.Add(time.Duration(i) * time.Minute).UTC().Format(time.RFC3339Nano)
		if _, err := db.Exec(`INSERT INTO turns(turn_id, checkout_id, session_id, marker, state, version, outcome, stub, request, created_at, updated_at)
			VALUES(?, ?, 's', ?, 'captured', 1, 'completed', 0, ?, ?, ?)`, fmt.Sprintf("t%03d", i), co, fmt.Sprintf("m%d", i), fmt.Sprintf("request number %d", i), at, at); err != nil {
			t.Fatal(err)
		}
	}
	got := h.context(t, h.dir)
	if !strings.Contains(got, "pending 60 ") {
		t.Fatalf("the pending count stopped at the row limit: %s", got)
	}
	rendered := strings.Count(got, "[captured] request number")
	m := regexp.MustCompile(`(\d+) more available`).FindStringSubmatch(got)
	if rendered == 0 || m == nil || m[1] != fmt.Sprint(60-rendered) {
		t.Fatalf("rendered=%d more=%v: 'more available' does not use the true count: %s", rendered, m, got)
	}
}

// A claim that could not be evaluated (here the deadline had passed) is unknown, never
// presented as not stale.
func TestStaleCountDoesNotFailOpen(t *testing.T) {
	h := newHookRig(t)
	p, e := openRigProject(t, h)
	ctx := context.Background()
	late, err := buildBriefing(ctx, p, e, "full", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(late, "stale unknown") || strings.Contains(late, "stale 0") {
		t.Fatalf("a claim that was never evaluated counted as not stale: %s", late)
	}
	ontime, err := buildBriefing(ctx, p, e, "full", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ontime, "stale 0") || strings.Contains(ontime, "unknown") {
		t.Fatalf("an evaluated claim was reported unknown: %s", ontime)
	}
}

// The mode in the briefing is the probed one, cached for the process.
func TestBriefingModeIsTheProbedMode(t *testing.T) {
	for version, mode := range map[string]string{"2.1.316": "full", "9.9.9": "reduced", "": "reduced"} {
		h := newHookRig(t)
		calls := stubClaudeVersion(t, version)
		if got := h.context(t, h.dir); !strings.HasPrefix(got, "wikory: mode="+mode+" ") {
			t.Fatalf("claude %q: want mode=%s: %s", version, mode, got)
		}
		if out, _, _ := h.c.run(h.dir, "context"); !strings.HasPrefix(out, "wikory: mode="+mode+" ") {
			t.Fatalf("claude %q: wikory context: want mode=%s: %s", version, mode, out)
		}
		if n := calls.Load(); n != 1 {
			t.Fatalf("claude %q: the probe ran %d times in one process, want once", version, n)
		}
	}
}

// Index problems reach every surface: the briefing, wikory context, search and doctor.
func TestIndexNoticesReachBriefingSearchAndDoctor(t *testing.T) {
	h := newHookRig(t)
	bad := "# Bad\n## H\n<!-- claim kind=guess status=unverified -->\nA malformed claim about retries.\n"
	if err := os.WriteFile(filepath.Join(h.dir, "project-memory", "wiki", "broken.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	huge := "# Huge\n\n" + strings.Repeat("x", 1<<20+10)
	if err := os.WriteFile(filepath.Join(h.dir, "project-memory", "wiki", "huge.md"), []byte(huge), 0o644); err != nil {
		t.Fatal(err)
	}
	surfaces := map[string]string{"briefing": h.context(t, h.dir)}
	surfaces["context"], _, _ = h.c.run(h.dir, "context")
	surfaces["search"], _, _ = h.c.run(h.dir, "search", "retries")
	surfaces["doctor"] = strings.Join(doctorFull(t, h.c, h.dir).Notices, "\n")
	for name, text := range surfaces {
		if !strings.Contains(text, "broken.md") || !strings.Contains(text, "huge.md") {
			t.Fatalf("%s does not disclose the pages the index dropped: %s", name, text)
		}
	}
}

func TestDoctorSurfacesFailedQueriesAndStuckJobs(t *testing.T) {
	h := newHookRig(t)
	db, co := h.storeDB(t), h.checkoutID(t)
	hourAgo := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO jobs(job_id, checkout_id, kind, state, epoch, fingerprint, detail, created_at, updated_at)
		VALUES('stuck-1', ?, 'publish', 'applying', 1, 'fp', '', ?, ?)`, co, hourAgo, hourAgo); err != nil {
		t.Fatal(err)
	}
	e := doctorFull(t, h.c, h.dir)
	if !anyContains(e.Notices, "wikory recover") || e.Data["stuck_jobs"] != float64(1) {
		t.Fatalf("a stuck publish job went unreported: stuck=%v notices=%v", e.Data["stuck_jobs"], e.Notices)
	}
	if got := h.context(t, h.dir); !strings.Contains(got, "failed 1 ") {
		t.Fatalf("a stuck job is not in the briefing's failed count: %s", got)
	}
	// A fresh job is in flight, not stuck.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE jobs SET created_at = ?, updated_at = ?`, now, now); err != nil {
		t.Fatal(err)
	}
	if e := doctorFull(t, h.c, h.dir); anyContains(e.Notices, "wikory recover") {
		t.Fatalf("a job begun a moment ago was called stuck: %v", e.Notices)
	}
	// A query that fails must be reported, not read as zero.
	if _, err := db.Exec(`ALTER TABLE jobs RENAME TO jobs_gone`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE VIEW jobs AS SELECT * FROM table_that_does_not_exist`); err != nil {
		t.Fatal(err)
	}
	e = doctorFull(t, h.c, h.dir)
	if !anyContains(e.Notices, "could not count conflicted jobs") || !anyContains(e.Notices, "could not count stuck jobs") {
		t.Fatalf("doctor read a failing query as all clear: %v", e.Notices)
	}
}

func TestDoctorCountsTurnsByStateAndIncompleteSeals(t *testing.T) {
	h := newHookRig(t)
	db, co := h.storeDB(t), h.checkoutID(t)
	at := time.Now().UTC().Format(time.RFC3339Nano)
	for _, x := range []struct{ id, state string }{{"cap1", "captured"}, {"seal1", "sealed"}, {"seal2", "sealed"}} {
		v := 1
		if x.state == "sealed" {
			v = 2
		}
		if _, err := db.Exec(`INSERT INTO turns(turn_id, checkout_id, session_id, marker, state, version, outcome, stub, request, created_at, updated_at)
			VALUES(?, ?, 's', ?, ?, ?, 'completed', 0, 'r', ?, ?)`, x.id, co, x.id, x.state, v, at, at); err != nil {
			t.Fatal(err)
		}
	}
	for id, incomplete := range map[string]bool{"seal1": true, "seal2": false} {
		dir := filepath.Join(h.storeDir(t), "turns", co, id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		rec := capture.Record{Schema: capture.RecordSchema, TurnID: id, Version: 2, State: "sealed", Incomplete: incomplete}
		if incomplete {
			rec.IncompleteReason = capture.ReasonUnreadable
		}
		b, _ := json.Marshal(rec)
		if err := os.WriteFile(filepath.Join(dir, "v0002.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e := doctorFull(t, h.c, h.dir)
	if e.Data["captured_turns"] != float64(1) || e.Data["sealed_turns"] != float64(2) || e.Data["incomplete_turns"] != float64(1) || !anyContains(e.Notices, "sealed incomplete") {
		t.Fatalf("captured=%v sealed=%v incomplete=%v notices=%v", e.Data["captured_turns"], e.Data["sealed_turns"], e.Data["incomplete_turns"], e.Notices)
	}
}

// If git status itself fails, doctor says it does not know, rather than listing no changes.
func TestDoctorSaysWhenGitStatusFails(t *testing.T) {
	h := newHookRig(t)
	if err := os.WriteFile(filepath.Join(h.dir, ".git", "index"), []byte("not an index"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := doctorFull(t, h.c, h.dir)
	if !anyContains(e.Notices, "could not read git status") {
		t.Fatalf("a failed git status was read as no uncommitted changes: %v", e.Notices)
	}
}
