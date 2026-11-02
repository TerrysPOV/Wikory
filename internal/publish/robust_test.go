package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (f *fixture) manifestPath(id string) string {
	return filepath.Join(f.st.Dir, "jobs", id, "manifest.json")
}

func (f *fixture) manifestState(t *testing.T, id string) string {
	t.Helper()
	data, err := os.ReadFile(f.manifestPath(id))
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m.State
}

// The database is authoritative: a job it calls final is skipped, never taken over again,
// and the manifest is rewritten to match. Before the fix takeoverLocked returned ErrJobState
// and aborted the whole Recover (G4 item 2a).
func TestRecoverSkipsAJobTheDatabaseCallsFinal(t *testing.T) {
	ctx := context.Background()
	for _, state := range []string{"conflict", "refused", "abandoned"} {
		t.Run(state, func(t *testing.T) {
			f, id := runKilled(t, "marked-0") // manifest says applying
			if _, err := f.st.DB.Exec(`UPDATE jobs SET state = ? WHERE job_id = ?`, state, id); err != nil {
				t.Fatal(err)
			}
			rep, err := f.publisher("co-1").Recover(ctx)
			b, _ := f.readRepo(t, "project-memory/wiki/b.md")
			if err != nil || b != "B0" || len(rep.Completed)+len(rep.Conflicts)+len(rep.Refused)+len(rep.Errors) != 0 {
				t.Fatalf("a job the database calls %s was not skipped cleanly: rep=%+v err=%v b=%q", state, rep, err, b)
			}
			if got := f.manifestState(t, id); got != state {
				t.Fatalf("manifest was not brought in line with the database: %q, want %q", got, state)
			}
			if _, err := f.publisher("co-1").Recover(ctx); err != nil {
				t.Fatalf("a second recovery failed: %v", err)
			}
		})
	}
}

// One unreadable manifest must not stop the good jobs behind it (G4 item 2b). The corrupt
// directory sorts first, so a Recover that returned on the first error would never reach the
// good job.
func TestRecoverContinuesPastACorruptManifest(t *testing.T) {
	ctx := context.Background()
	f, id := runKilled(t, "manifest")
	bad := filepath.Join(f.st.Dir, "jobs", "0-corrupt")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "manifest.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := f.publisher("co-1").Recover(ctx)
	a, _ := f.readRepo(t, "project-memory/wiki/a.md")
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if a != "A1" || b != "B1" || len(rep.Completed) != 1 || rep.Completed[0] != id || f.jobState(t, id) != "done" {
		t.Fatalf("the good job was not recovered past the corrupt one: rep=%+v a=%q b=%q", rep, a, b)
	}
	if len(rep.Errors) != 1 || rep.Errors[0].JobID != "0-corrupt" || err == nil || !strings.Contains(err.Error(), "0-corrupt") {
		t.Fatalf("the corrupt job is not named in the report and the error: rep.Errors=%+v err=%v", rep.Errors, err)
	}
}

// A conflict whose record cannot be written must say so: the caller still sees ErrConflict
// plus the recording failure (G4 item 2c).
func TestConflictReportsARecordingFailure(t *testing.T) {
	ctx := context.Background()
	t.Run("database", func(t *testing.T) {
		f := newFixture(t)
		f.failConflictRecording(t)
		setBeforeApplyHook(t, func(i int) {
			if i == 1 {
				f.writeRepo(t, "project-memory/wiki/b.md", "EDITED DURING APPLY")
			}
		})
		p := f.publisher("co-1")
		j, _ := p.Begin(ctx, "test")
		err := p.Publish(ctx, j, standardOps(), "x")
		if !errors.Is(err, ErrConflict) || !strings.Contains(fmt.Sprint(err), "boom") || !strings.Contains(fmt.Sprint(err), "database") {
			t.Fatalf("the failure to record the conflict in the database was discarded: %v", err)
		}
	})
	t.Run("manifest", func(t *testing.T) {
		f := newFixture(t)
		p := f.publisher("co-1")
		j, _ := p.Begin(ctx, "test")
		setBeforeApplyHook(t, func(i int) {
			if i == 1 {
				f.writeRepo(t, "project-memory/wiki/b.md", "EDITED DURING APPLY")
				mp := f.manifestPath(j.ID)
				if err := os.Remove(mp); err != nil {
					t.Error(err)
				}
				if err := os.Mkdir(mp, 0o700); err != nil { // the manifest can no longer be rewritten
					t.Error(err)
				}
			}
		})
		err := p.Publish(ctx, j, standardOps(), "x")
		if !errors.Is(err, ErrConflict) || !strings.Contains(fmt.Sprint(err), "manifest") || f.jobState(t, j.ID) != "conflict" {
			t.Fatalf("the failure to record the conflict in the manifest was discarded: err=%v state=%s", err, f.jobState(t, j.ID))
		}
	})
}

// A publisher for another checkout must not fence this checkout's job (G4 item 3).
func TestTakeoverRefusesAnotherCheckoutsJob(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	j, _ := f.publisher("co-1").Begin(ctx, "test")
	_, err := f.publisher("co-2").Takeover(ctx, j.ID)
	var epoch int64
	_ = f.st.DB.QueryRow(`SELECT epoch FROM jobs WHERE job_id = ?`, j.ID).Scan(&epoch)
	if !errors.Is(err, ErrWrongCheckout) || epoch != j.Epoch {
		t.Fatalf("co-2 fenced co-1's job: err=%v epoch %d -> %d", err, j.Epoch, epoch)
	}
	if err := f.publisher("co-1").Publish(ctx, j, standardOps(), "x"); err != nil {
		t.Fatalf("the rightful holder was fenced: %v", err)
	}
}

// Two operations on one target would apply the first and then conflict with it (G4 item 4).
func TestDuplicateTargetsAreRejectedBeforeAnythingIsWritten(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	p := f.publisher("co-1")
	j, _ := p.Begin(ctx, "test")
	b0 := HashContent([]byte("B0"))
	ops := []Op{
		{Target: "project-memory/wiki/b.md", OldHash: b0, Content: []byte("B1")},
		{Target: "project-memory/wiki/b.md", OldHash: b0, Content: []byte("B2")},
	}
	err := p.Publish(ctx, j, ops, "x")
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if !errors.Is(err, ErrDuplicateTarget) || b != "B0" || f.jobState(t, j.ID) != "begun" {
		t.Fatalf("err=%v b=%q state=%s", err, b, f.jobState(t, j.ID))
	}
}

// A zero-value Publisher must be refused with a clear error: a nil Now or Fingerprint panics,
// and AbandonAfter 0 would abandon live jobs (G4 item 4).
func TestZeroValuePublisherIsRefusedNotUsed(t *testing.T) {
	ctx := context.Background()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("a zero-value Publisher panicked: %v", r)
		}
	}()
	if _, err := (&Publisher{}).Begin(ctx, "test"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Begin on a zero-value Publisher: want ErrNotConfigured, got %v", err)
	}
	if _, err := (&Publisher{}).Recover(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Recover on a zero-value Publisher: want ErrNotConfigured, got %v", err)
	}
	f := newFixture(t)
	live, _ := f.publisher("co-1").Begin(ctx, "test")
	noAbandon := f.publisher("co-1")
	noAbandon.AbandonAfter = 0
	rep, err := noAbandon.Recover(ctx)
	if !errors.Is(err, ErrNotConfigured) || len(rep.Abandoned) != 0 || f.jobState(t, live.ID) != "begun" {
		t.Fatalf("AbandonAfter 0 abandoned a live job: rep=%+v err=%v state=%s", rep, err, f.jobState(t, live.ID))
	}
}

func bigLog(n int) string { return strings.Repeat("x", n) }

// A log over the cap is rotated, never turned into a failed publish (G4 item 5). The log is
// one byte over the cap, so it exceeds the bound it is judged against.
func TestOversizeLogIsRotatedNotFatal(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	old := bigLog(maxFile + 1)
	f.writeRepo(t, "project-memory/log.md", old)
	p := f.publisher("co-1")
	j, _ := p.Begin(ctx, "test")
	err := p.Publish(ctx, j, standardOps(), "after a full log")
	rotated, _ := f.readRepo(t, "project-memory/log.1.md")
	if err != nil || f.jobState(t, j.ID) != "done" || f.logLines(t, j.ID) != 1 || rotated != old {
		t.Fatalf("a full log failed the publish: err=%v state=%s log=%d rotated=%d bytes", err, f.jobState(t, j.ID), f.logLines(t, j.ID), len(rotated))
	}
}

// An oversize log that already holds this job's line is neither rotated nor given a second
// line. The line sits across a read-chunk boundary, so the search must overlap its chunks.
func TestOversizeLogWithTheJobLineIsLeftAlone(t *testing.T) {
	f := newFixture(t)
	p := f.publisher("co-1")
	m := &manifest{JobID: "job_STRADDLE", LogLine: "x"}
	// "job=job_STRADDLE" starts 3 bytes before the end of the first chunk.
	const prefix = "- t "
	log := bigLog(logScanChunk-3-len(prefix)) + prefix + "job=job_STRADDLE x\n" + bigLog(maxFile)
	f.writeRepo(t, "project-memory/log.md", log)
	if err := p.appendLogOnce(m); err != nil {
		t.Fatal(err)
	}
	after, _ := f.readRepo(t, "project-memory/log.md")
	_, rotated := f.readRepo(t, "project-memory/log.1.md")
	if after != log || rotated {
		t.Fatalf("the job's line was not found in an oversize log: unchanged=%v rotated=%v", after == log, rotated)
	}
}

// stageOps writes the staged files before the manifest, so a job killed between the two
// leaves a stage directory that recovery must remove once it abandons the job (G4 item 9).
func TestAbandonedJobsStageDirectoryIsRemoved(t *testing.T) {
	ctx := context.Background()
	f, id := runKilled(t, "staged")
	stage := filepath.Join(f.st.Dir, "jobs", id, "stage")
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("the killed job left no stage directory to remove: %v", err)
	}
	if _, err := f.publisher("co-1").Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) || f.jobState(t, id) != "abandoned" {
		t.Fatalf("stage directory survived recovery: stat err=%v state=%s", err, f.jobState(t, id))
	}
}

// Backups recovery had to leave beside a human file are reported, not discarded (G4 item 7).
func TestRecoveryReportsKeptBackups(t *testing.T) {
	ctx := context.Background()
	f, _ := runKilled(t, "moved-1")
	f.writeRepo(t, "project-memory/wiki/b.md", "HUMAN EDIT")
	rep, err := f.publisher("co-1").Recover(ctx)
	if err != nil || len(rep.Kept) != 1 || !strings.Contains(rep.Kept[0], "project-memory/wiki/.wikory-backup-b.md-") {
		t.Fatalf("the kept backup is not reported: rep=%+v err=%v", rep, err)
	}
}

// Recovery that finds a conflict it cannot record still reports the conflict, and also says
// the recording failed (G4 item 2c, recovery path).
func TestRecoveryReportsAConflictItCouldNotRecord(t *testing.T) {
	ctx := context.Background()
	f, id := runKilled(t, "marked-0")
	f.writeRepo(t, "project-memory/wiki/b.md", "HUMAN EDIT")
	f.failConflictRecording(t)
	rep, err := f.publisher("co-1").Recover(ctx)
	if len(rep.Conflicts) != 1 || len(rep.Errors) != 1 || rep.Errors[0].JobID != id || err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("the unrecorded conflict is not reported: rep=%+v err=%v", rep, err)
	}
}
