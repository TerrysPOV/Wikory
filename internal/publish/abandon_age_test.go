package publish

import (
	"context"
	"testing"
	"time"
)

// clockedPublisher is a publisher whose clock the test moves, with the real abandon age.
func (f *fixture) clockedPublisher(now *time.Time) *Publisher {
	p := f.publisher("co-1")
	p.AbandonAfter = 10 * time.Minute
	p.Now = func() time.Time { return *now }
	return p
}

// A job younger than AbandonAfter belongs to a live process and must survive recovery; one older
// than it is abandoned. The ages (1 minute, 11 minutes) sit either side of the 10 minute bound.
func TestAbandonStaleHonoursTheAgeBound(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	p := f.clockedPublisher(&now)
	j, err := p.Begin(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	rep, err := p.Recover(ctx)
	if err != nil || len(rep.Abandoned) != 0 || f.jobState(t, j.ID) != "begun" {
		t.Fatalf("a fresh job was abandoned: rep=%+v err=%v state=%s", rep, err, f.jobState(t, j.ID))
	}

	now = now.Add(10 * time.Minute) // 11 minutes old
	rep, err = p.Recover(ctx)
	if err != nil || len(rep.Abandoned) != 1 || rep.Abandoned[0] != j.ID || f.jobState(t, j.ID) != "abandoned" {
		t.Fatalf("an old job was not abandoned: rep=%+v err=%v state=%s", rep, err, f.jobState(t, j.ID))
	}
}

// A created_at that does not parse must not read as the zero time (which is ancient) and so
// abandon a job that may be live: the job is kept and the failure is reported.
func TestAbandonStaleKeepsAndReportsAnUnparseableAge(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	p := f.clockedPublisher(&now)
	j, err := p.Begin(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB.Exec(`UPDATE jobs SET created_at = 'not a time' WHERE job_id = ?`, j.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	rep, err := p.Recover(ctx)
	if len(rep.Abandoned) != 0 || f.jobState(t, j.ID) != "begun" {
		t.Fatalf("a job with an unreadable age was abandoned: rep=%+v state=%s", rep, f.jobState(t, j.ID))
	}
	if err == nil || len(rep.Errors) != 1 || rep.Errors[0].JobID != j.ID {
		t.Fatalf("the unreadable age was not reported against the job: rep=%+v err=%v", rep, err)
	}
}
