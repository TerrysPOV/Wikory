package publish

import (
	"context"
	"errors"
	"strings"
	"sync"
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
	if st := f.jobState(t, j.ID); st != "refused" { // the error alone would leave the job open for recovery to retry
		t.Fatalf("job state %q after a fingerprint refusal, want refused", st)
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
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	killHook = func(step string) {
		if step == "marked-0" {
			once.Do(func() { close(reached) })
			<-release // the publish holds the lock until the test lets it go
		}
	}
	t.Cleanup(func() { killHook = func(string) {} })
	pubErr := make(chan error, 1)
	go func() { pubErr <- p.Publish(ctx, j, standardOps(), "in progress") }()
	<-reached
	type result struct {
		err error
	}
	taken := make(chan result, 1)
	go func() {
		_, err := f.publisher("co-1").Takeover(ctx, j.ID)
		taken <- result{err}
	}()
	// A takeover that ignores the lock returns within milliseconds, so a returned result inside
	// this window is a failure. A slow machine only makes a correct takeover wait longer, never
	// return early, so the window cannot fail a correct implementation.
	select {
	case r := <-taken:
		close(release)
		t.Fatalf("takeover returned while the publish still held the lock: err=%v", r.err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	if err := <-pubErr; err != nil {
		t.Fatalf("the publish in progress was fenced mid-way: %v", err)
	}
	if r := <-taken; !errors.Is(r.err, ErrJobState) {
		t.Fatalf("takeover after the publish finished: want ErrJobState, got %v", r.err)
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
	a, _ := f.readRepo(t, "project-memory/wiki/a.md")
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if !errors.Is(err, ErrConflict) || b != "EDITED DURING APPLY" || f.jobState(t, j.ID) != "conflict" {
		t.Fatalf("err=%v b=%q state=%s", err, b, f.jobState(t, j.ID))
	}
	// Exactly which files changed: op 0 was applied before op 1 conflicted, op 1 kept the
	// human's edit, and the log line was never written. The message must not claim otherwise.
	if a != "A1" || f.logLines(t, j.ID) != 0 {
		t.Fatalf("a=%q (want the applied A1) log=%d (want 0)", a, f.logLines(t, j.ID))
	}
	if !strings.Contains(err.Error(), "earlier operations of this job may already be applied") {
		t.Fatalf("the conflict message does not warn that earlier operations may be applied: %v", err)
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
