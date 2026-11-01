package publish

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPublishAppliesOpsAndLogsOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	p := f.publisher("co-1")
	j, err := p.Begin(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Publish(ctx, j, standardOps(), "test publish"); err != nil {
		t.Fatal(err)
	}
	a, _ := f.readRepo(t, "project-memory/wiki/a.md")
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if a != "A1" || b != "B1" || f.logLines(t, j.ID) != 1 || f.jobState(t, j.ID) != "done" {
		t.Fatalf("a=%q b=%q log=%d state=%s", a, b, f.logLines(t, j.ID), f.jobState(t, j.ID))
	}
	if err := p.Publish(ctx, j, standardOps(), "again"); !errors.Is(err, ErrJobState) {
		t.Fatalf("replaying a finished job: want ErrJobState, got %v", err)
	}
	if f.logLines(t, j.ID) != 1 {
		t.Fatal("a replay appended a second log line")
	}
}

func TestStaleEpochIsFenced(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	p := f.publisher("co-1")
	j, _ := p.Begin(ctx, "test")
	j2, err := p.Takeover(ctx, j.ID)
	if err != nil || j2.Epoch != j.Epoch+1 {
		t.Fatalf("takeover %+v %v", j2, err)
	}
	if err := p.Publish(ctx, j, standardOps(), "old holder"); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("want ErrStaleEpoch, got %v", err)
	}
	if _, ok := f.readRepo(t, "project-memory/wiki/a.md"); ok {
		t.Fatal("the fenced holder wrote a file")
	}
	if err := p.Publish(ctx, j2, standardOps(), "new holder"); err != nil {
		t.Fatal(err)
	}
}

func TestBaseHashMismatchIsAConflict(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	p := f.publisher("co-1")
	j, _ := p.Begin(ctx, "test")
	f.writeRepo(t, "project-memory/wiki/b.md", "EDITED BY A HUMAN")
	if err := p.Publish(ctx, j, standardOps(), "x"); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if _, ok := f.readRepo(t, "project-memory/wiki/a.md"); ok || b != "EDITED BY A HUMAN" {
		t.Fatalf("a refused publish changed files: b=%q", b)
	}
}

func TestFingerprintChangeRefuses(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	p := f.publisher("co-1")
	j, _ := p.Begin(ctx, "test")
	f.fp = "tree-2" // a branch switch while the job was open
	if err := p.Publish(ctx, j, standardOps(), "x"); !errors.Is(err, ErrFingerprint) {
		t.Fatalf("want ErrFingerprint, got %v", err)
	}
}

func TestWrongCheckoutRefused(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	j, _ := f.publisher("co-1").Begin(ctx, "test")
	if err := f.publisher("co-2").Publish(ctx, j, standardOps(), "x"); !errors.Is(err, ErrWrongCheckout) {
		t.Fatalf("want ErrWrongCheckout, got %v", err)
	}
}

func TestSymlinkedTargetIsAConflict(t *testing.T) {
	if !symlinksAvailable() {
		t.Skip("symlinks need developer mode on Windows")
	}
	ctx := context.Background()
	f := newFixture(t)
	outside := t.TempDir() + "/outside.md"
	f.writeAbs(t, outside, "OUTSIDE")
	f.symlinkRepo(t, outside, "project-memory/wiki/a.md")
	p := f.publisher("co-1")
	j, _ := p.Begin(ctx, "test")
	err := p.Publish(ctx, j, []Op{{Target: "project-memory/wiki/a.md", OldHash: Absent, Content: []byte("A1")}}, "x")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if got := f.readAbs(t, outside); got != "OUTSIDE" {
		t.Fatalf("the symlink target was written: %q", got)
	}
}

// Takeover runs under the publishing lock: it waits for a publish in progress to finish and
// can never fence it between two of its steps.
func TestTakeoverWaitsForThePublishingLock(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	p := f.publisher("co-1")
	j, _ := p.Begin(ctx, "test")
	reached := make(chan struct{})
	killHook = func(step string) {
		if step == "marked-0" {
			close(reached)
			time.Sleep(300 * time.Millisecond)
		}
	}
	t.Cleanup(func() { killHook = func(string) {} })
	pubErr := make(chan error, 1)
	go func() { pubErr <- p.Publish(ctx, j, standardOps(), "in progress") }()
	<-reached
	start := time.Now()
	_, takeErr := f.publisher("co-1").Takeover(ctx, j.ID)
	waited := time.Since(start)
	if err := <-pubErr; err != nil {
		t.Fatalf("the publish in progress was fenced mid-way: %v", err)
	}
	if !errors.Is(takeErr, ErrJobState) || waited < 200*time.Millisecond {
		t.Fatalf("takeover did not wait for the lock: err=%v waited=%v", takeErr, waited)
	}
}

// A human edit between the stage and the apply of an op: the move-aside compare must catch
// it, the file keeps the edit, and the job is a conflict.
func TestEditBetweenStageAndApplyIsAConflict(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	testHookBeforeApply = func(i int) {
		if i == 1 {
			f.writeRepo(t, "project-memory/wiki/b.md", "EDITED DURING APPLY")
		}
	}
	t.Cleanup(func() { testHookBeforeApply = func(int) {} })
	p := f.publisher("co-1")
	j, _ := p.Begin(ctx, "test")
	err := p.Publish(ctx, j, standardOps(), "x")
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if !errors.Is(err, ErrConflict) || b != "EDITED DURING APPLY" || f.jobState(t, j.ID) != "conflict" {
		t.Fatalf("err=%v b=%q state=%s", err, b, f.jobState(t, j.ID))
	}
}

func TestOpsAreMarkedInTheDatabase(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	p := f.publisher("co-1")
	j, _ := p.Begin(ctx, "test")
	if err := p.Publish(ctx, j, standardOps(), "x"); err != nil {
		t.Fatal(err)
	}
	var done int
	_ = f.st.DB.QueryRow(`SELECT count(*) FROM job_ops WHERE job_id = ? AND state = 'done'`, j.ID).Scan(&done)
	if done != 2 {
		t.Fatalf("db marks: %d of 2 ops done", done)
	}
}
