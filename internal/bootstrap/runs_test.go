package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/store"
)

type lockCount struct{ n int }

// newTestRuns opens a real store and returns Runs whose clock the test controls and whose lock
// calls are counted.
func newTestRuns(t *testing.T) (*Runs, *store.Store, *time.Time, *lockCount) {
	t.Helper()
	ctx := context.Background()
	id := store.Identity{ProjectID: "wkp_P", CommonDir: "/c/.git", Worktree: "/c"}
	s, err := store.Resolve(ctx, t.TempDir(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	co, err := s.Register(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	lc := &lockCount{}
	lock := func(ctx context.Context, fn func(context.Context) error) error {
		lc.n++
		return s.WithWriteLock(ctx, fn)
	}
	r := NewRuns(s.DB, co.ID, lock)
	r.Now = func() time.Time { return now }
	return r, s, &now, lc
}

func specsN(n int) []ItemSpec {
	out := make([]ItemSpec, n)
	for i := range out {
		out[i] = ItemSpec{Stage: "extract", Ref: "src:repo:f" + string(rune('a'+i)) + ".md", InputHash: "h"}
	}
	return out
}

func planWith(sample int) Plan {
	res := sampleResult()
	return BuildPlan(res, "repo", "head1", sample)
}

func mustRun(t *testing.T, r *Runs, sample, items int) Run {
	t.Helper()
	run, err := r.Create(context.Background(), planWith(sample), specsN(items))
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestCreatePersistsTheRunItsSourcesComponentsAndItems(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 4)
	got, err := r.Get(ctx, run.ID)
	if err != nil || got.State != StatePlanned || got.PlanHash != planWith(0).Hash || got.Head != "head1" {
		t.Fatalf("%+v %v", got, err)
	}
	srcs, _ := r.Sources(ctx, run.ID)
	comps, _ := r.Components(ctx, run.ID)
	if len(srcs) != len(sampleResult().Sources) || len(comps) != 2 || !comps[0].Inspected {
		t.Fatalf("sources %d components %+v", len(srcs), comps)
	}
	st, _ := r.Status(ctx, run.ID)
	if st.Items["extract"]["pending"] != 4 {
		t.Fatalf("%+v", st.Items)
	}
}

// The sample size is not part of the plan hash, so the hash cannot be what binds a run to the
// sample it was planned with: the run must store it, and its sampled items are fixed at Create.
func TestTheRunStoresTheSampleSizeAndFixesItsSampledItemsAtCreate(t *testing.T) {
	ctx := context.Background()
	r, s, _, _ := newTestRuns(t)
	if planWith(0).Hash != planWith(3).Hash {
		t.Fatal("precondition: the sample size is outside the plan hash, which is why the run must store it")
	}
	for _, want := range []int{0, 3} {
		run, err := r.Create(ctx, planWith(want), specsN(9))
		if err != nil {
			t.Fatal(err)
		}
		got, err := r.Get(ctx, run.ID)
		if err != nil || got.Sample != want || run.Sample != want {
			t.Fatalf("the run must keep the sample size it was planned with: want %d, stored %d, returned %d (%v)", want, got.Sample, run.Sample, err)
		}
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM bs_items WHERE run_id=? AND sampled=1`, run.ID).Scan(&n); err != nil || n != want {
			t.Fatalf("sample %d must flag %d items at Create, flagged %d (%v)", want, want, n, err)
		}
	}
}

func TestTheSourceDirectoriesARunWasPlannedWithAreStored(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	p := planWith(0)
	p.Roots = map[string]string{"notes": "/work/notes", "adr": "/work/adr"}
	run, err := r.Create(ctx, p, specsN(1))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Roots(ctx, run.ID)
	if err != nil || len(got) != 2 || got["notes"] != "/work/notes" || got["adr"] != "/work/adr" {
		t.Fatalf("%v %v", got, err)
	}
	other := *r
	other.CheckoutID = "someone-else"
	if _, err := other.Roots(ctx, run.ID); !errors.Is(err, ErrNoRun) {
		t.Fatalf("roots are bound to the checkout too: %v", err)
	}
}

func TestSourceDatesSurviveTheRunStoreAndStayOutOfThePlanHash(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	a, b := sampleResult(), sampleResult()
	a.Sources[0].Date = "2026-03-04T05:06:07Z"
	b.Sources[0].Date = "2019-01-01T00:00:00Z"
	pa := BuildPlan(a, "repo", "head1", 0)
	if pa.Hash != BuildPlan(b, "repo", "head1", 0).Hash {
		t.Fatal("a source's date is for the report; it must not change the plan hash")
	}
	run, err := r.Create(ctx, pa, specsN(1))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Sources(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range got {
		if s.Path == a.Sources[0].Path {
			found = true
			if s.Date != "2026-03-04T05:06:07Z" {
				t.Fatalf("the stored source lost its date: %q", s.Date)
			}
		}
	}
	if !found {
		t.Fatal("source missing")
	}
}

func TestARunOfAnotherCheckoutIsInvisible(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 1)
	other := *r
	other.CheckoutID = "someone-else"
	if _, err := other.Get(ctx, run.ID); !errors.Is(err, ErrNoRun) {
		t.Fatalf("a run is bound to its checkout: %v", err)
	}
	if _, err := other.Next(ctx, run.ID, 1); !errors.Is(err, ErrNoRun) {
		t.Fatalf("Next must not lease another checkout's work: %v", err)
	}
}

func TestNextRefusesARunThatIsNotConfirmed(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 2)
	if _, err := r.Next(ctx, run.ID, 2); !errors.Is(err, ErrState) {
		t.Fatalf("a planned run must not hand out work: %v", err)
	}
	if err := r.Transition(ctx, run.ID, StateRunning, StatePlanned); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(ctx, run.ID, 2); err != nil {
		t.Fatal(err)
	}
}

func TestNextLeasesInOrderOnceAndBumpsTheEpoch(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 5)
	_ = r.Transition(ctx, run.ID, StateRunning, StatePlanned)
	b1, err := r.Next(ctx, run.ID, 3)
	if err != nil || len(b1.Items) != 3 || b1.ID == "" {
		t.Fatalf("%+v %v", b1, err)
	}
	for i, it := range b1.Items {
		if it.Epoch != 1 || it.State != "leased" || it.Ref != specsN(5)[i].Ref {
			t.Fatalf("item %d: %+v", i, it)
		}
	}
	b2, _ := r.Next(ctx, run.ID, 3)
	if len(b2.Items) != 2 {
		t.Fatalf("the second call gets only what is left: %+v", b2)
	}
	b3, _ := r.Next(ctx, run.ID, 3)
	if len(b3.Items) != 0 || b3.ID != "" {
		t.Fatalf("nothing left to lease: %+v", b3)
	}
}

func TestSamplingLeasesOnlyTheSampledItemsAndThenAwaitsConfirmation(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 3, 9)
	_ = r.Transition(ctx, run.ID, StateSampling, StatePlanned)
	b, _ := r.Next(ctx, run.ID, 50)
	if len(b.Items) != 3 || b.Items[0].Ref != specsN(9)[0].Ref || b.Items[1].Ref != specsN(9)[3].Ref || b.Items[2].Ref != specsN(9)[6].Ref {
		t.Fatalf("the sample is an even spread (0, 3, 6): %+v", b.Items)
	}
	for _, it := range b.Items {
		if err := r.Complete(ctx, it.ID, it.Epoch, Usage{InputTokens: 100, OutputTokens: 10}, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := r.Get(ctx, run.ID)
	if got.State != StateAwaiting || got.TokensIn != 300 || got.TokensOut != 30 {
		t.Fatalf("finishing the sample moves the run to awaiting-confirm with measured usage: %+v", got)
	}
	if _, err := r.Next(ctx, run.ID, 1); !errors.Is(err, ErrState) {
		t.Fatalf("no more work until the person confirms: %v", err)
	}
}

func TestAnExpiredLeaseIsReclaimedAndAStaleCompleteIsRefused(t *testing.T) {
	ctx := context.Background()
	r, _, now, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 1)
	_ = r.Transition(ctx, run.ID, StateRunning, StatePlanned)
	first, _ := r.Next(ctx, run.ID, 1)
	*now = now.Add(LeaseTTL + time.Minute)
	second, _ := r.Next(ctx, run.ID, 1)
	if len(second.Items) != 1 || second.Items[0].ID != first.Items[0].ID || second.Items[0].Epoch != 2 || second.Items[0].Attempts != 1 {
		t.Fatalf("the crashed driver's item is re-leased at a higher epoch: %+v", second.Items)
	}
	if err := r.Complete(ctx, first.Items[0].ID, 1, Usage{}, nil); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("a late submit for the old epoch must be refused: %v", err)
	}
	if err := r.Complete(ctx, second.Items[0].ID, 2, Usage{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAnItemThatKeepsExpiringIsRejectedAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	r, _, now, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 1)
	_ = r.Transition(ctx, run.ID, StateRunning, StatePlanned)
	for i := 0; i < MaxAttempts; i++ {
		b, _ := r.Next(ctx, run.ID, 1)
		if len(b.Items) != 1 {
			t.Fatalf("attempt %d: %+v", i, b)
		}
		*now = now.Add(LeaseTTL + time.Minute)
	}
	b, _ := r.Next(ctx, run.ID, 1)
	st, _ := r.Status(ctx, run.ID)
	if len(b.Items) != 0 || st.Items["extract"]["rejected"] != 1 {
		t.Fatalf("a poison item must stop being re-leased: %+v %+v", b, st.Items)
	}
}

// A sample that cannot finish normally must still end: otherwise the run sits in sampling, Next
// has nothing to hand out, and the full run refuses to start.
func TestASampleWhoseLastItemIsRejectedStillSettles(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 1, 3)
	_ = r.Transition(ctx, run.ID, StateSampling, StatePlanned)
	for i := 0; i < MaxAttempts; i++ {
		b, _ := r.Next(ctx, run.ID, 5)
		if len(b.Items) != 1 {
			t.Fatalf("attempt %d: %+v", i, b)
		}
		if err := r.FailCounted(ctx, b.Items[0].ID, b.Items[0].Epoch, 0, 0, Usage{}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := r.Get(ctx, run.ID)
	if got.State != StateAwaiting {
		t.Fatalf("a sample whose only item was rejected must still reach awaiting-confirm, got %s", got.State)
	}
}

func TestASampleWhoseLastLeaseExpiresStillSettles(t *testing.T) {
	ctx := context.Background()
	r, _, now, _ := newTestRuns(t)
	run := mustRun(t, r, 1, 3)
	_ = r.Transition(ctx, run.ID, StateSampling, StatePlanned)
	for i := 0; i < MaxAttempts; i++ {
		b, _ := r.Next(ctx, run.ID, 5)
		if len(b.Items) != 1 {
			t.Fatalf("attempt %d: %+v", i, b)
		}
		*now = now.Add(LeaseTTL + time.Minute)
	}
	if b, _ := r.Next(ctx, run.ID, 5); len(b.Items) != 0 {
		t.Fatalf("the poison item is not leased again: %+v", b)
	}
	got, _ := r.Get(ctx, run.ID)
	if got.State != StateAwaiting {
		t.Fatalf("a sample whose last lease expired MaxAttempts times must still reach awaiting-confirm, got %s", got.State)
	}
}

func TestAnEmptySampleSettlesAtOnce(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 3, 0)
	if err := r.Transition(ctx, run.ID, StateSampling, StatePlanned); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(ctx, run.ID)
	if got.State != StateAwaiting {
		t.Fatalf("a sample with no items has nothing to wait for, got %s", got.State)
	}
}

func TestCompleteIsAtomicWithTheCallersWrites(t *testing.T) {
	ctx := context.Background()
	r, s, _, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 1)
	_ = r.Transition(ctx, run.ID, StateRunning, StatePlanned)
	b, _ := r.Next(ctx, run.ID, 1)
	boom := errors.New("boom")
	err := r.Complete(ctx, b.Items[0].ID, 1, Usage{InputTokens: 9}, func(ctx context.Context, tx *sql.Tx, it Item) error {
		if _, e := tx.ExecContext(ctx, `INSERT INTO bs_claims(run_id,checkout_id,item_id,ordinal,claim_id,text,kind,status,meta,tier) VALUES(?,?,?,0,'c','t','intent','unverified','','intent')`, it.RunID, r.CheckoutID, it.ID); e != nil {
			return e
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
	var n int
	_ = s.DB.QueryRow(`SELECT count(*) FROM bs_claims`).Scan(&n)
	st, _ := r.Status(ctx, run.ID)
	if n != 0 || st.Items["extract"]["leased"] != 1 || st.Run.TokensIn != 0 {
		t.Fatalf("a failed callback must roll back the claims, the item state and the usage: claims=%d %+v", n, st)
	}
}

func TestEveryMutationTakesTheWriteLock(t *testing.T) {
	ctx := context.Background()
	r, _, _, lc := newTestRuns(t)
	type step struct {
		name string
		do   func() error
	}
	var run Run
	var batch Batch
	steps := []step{
		{"Create", func() (err error) { run, err = r.Create(ctx, planWith(0), specsN(2)); return }},
		{"Transition", func() error { return r.Transition(ctx, run.ID, StateRunning, StatePlanned) }},
		{"Next", func() (err error) { batch, err = r.Next(ctx, run.ID, 1); return }},
		{"FailCounted", func() error { return r.FailCounted(ctx, batch.Items[0].ID, batch.Items[0].Epoch, 0, 0, Usage{}) }},
		{"Complete", func() error {
			b, err := r.Next(ctx, run.ID, 1)
			if err != nil || len(b.Items) != 1 {
				return fmt.Errorf("no item to complete: %v %v", b, err)
			}
			return r.Complete(ctx, b.Items[0].ID, b.Items[0].Epoch, Usage{}, nil)
		}},
	}
	for _, s := range steps {
		before := lc.n
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if lc.n == before {
			t.Errorf("%s wrote without taking the database write lock", s.name)
		}
	}
}

type figures struct{ sampleIn, sampleOut, sampleAcc, sampleRej, in, out, acc, rej int }

func readFigures(t *testing.T, s *store.Store, runID string) figures {
	t.Helper()
	var f figures
	err := s.DB.QueryRow(`SELECT sample_in,sample_out,sample_acc,sample_rej,tokens_in,tokens_out,accepted,rejected FROM bs_runs WHERE run_id=?`, runID).
		Scan(&f.sampleIn, &f.sampleOut, &f.sampleAcc, &f.sampleRej, &f.in, &f.out, &f.acc, &f.rej)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// The sample's cost is what Terry's go-ahead rests on, so it is read straight from the table.
// The sample here has two items: one completes with usage, the other fails its way to rejection
// on a FailCounted that carries the final counts. The frozen figures must include every count,
// the settling call's too, and must not move once the full run adds to the run's own counters.
func TestTheFrozenSampleFiguresIncludeTheSettlingFailureAndStayFrozen(t *testing.T) {
	ctx := context.Background()
	r, s, _, _ := newTestRuns(t)
	run := mustRun(t, r, 2, 4)
	if err := r.Transition(ctx, run.ID, StateSampling, StatePlanned); err != nil {
		t.Fatal(err)
	}
	first, err := r.Next(ctx, run.ID, 50)
	if err != nil || len(first.Items) != 2 {
		t.Fatalf("a sample of 2 leases 2 items: %+v %v", first, err)
	}
	if err := r.Complete(ctx, first.Items[0].ID, first.Items[0].Epoch, Usage{InputTokens: 100, OutputTokens: 10}, nil); err != nil {
		t.Fatal(err)
	}
	if f := readFigures(t, s, run.ID); f.sampleIn != 0 || f.sampleOut != 0 {
		t.Fatalf("nothing is frozen while a sampled item is still open: %+v", f)
	}
	bad := first.Items[1]
	if err := r.FailCounted(ctx, bad.ID, bad.Epoch, 4, 0, Usage{InputTokens: 40, OutputTokens: 4}); err != nil {
		t.Fatal(err)
	}
	for i, counts := range [][4]int{{0, 0, 20, 2}, {2, 1, 10, 1}} {
		b, err := r.Next(ctx, run.ID, 50)
		if err != nil || len(b.Items) != 1 || b.Items[0].ID != bad.ID {
			t.Fatalf("retry %d: %+v %v", i, b, err)
		}
		if err := r.FailCounted(ctx, b.Items[0].ID, b.Items[0].Epoch, counts[0], counts[1], Usage{InputTokens: counts[2], OutputTokens: counts[3]}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := r.Get(ctx, run.ID)
	if got.State != StateAwaiting {
		t.Fatalf("the settling failure must move the run to awaiting-confirm, got %s", got.State)
	}
	want := figures{sampleIn: 170, sampleOut: 17, sampleAcc: 6, sampleRej: 1, in: 170, out: 17, acc: 6, rej: 1}
	if f := readFigures(t, s, run.ID); f != want {
		t.Fatalf("the sample figures must hold the usage and every count including the settling failure's: got %+v want %+v", f, want)
	}

	if err := r.Transition(ctx, run.ID, StateRunning, StateAwaiting); err != nil {
		t.Fatal(err)
	}
	rest, err := r.Next(ctx, run.ID, 50)
	if err != nil || len(rest.Items) != 2 {
		t.Fatalf("the full run leases the two unsampled items: %+v %v", rest, err)
	}
	if err := r.Complete(ctx, rest.Items[0].ID, rest.Items[0].Epoch, Usage{InputTokens: 7, OutputTokens: 3}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.FailCounted(ctx, rest.Items[1].ID, rest.Items[1].Epoch, 5, 2, Usage{InputTokens: 5, OutputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	want.in, want.out, want.acc, want.rej = 182, 21, 11, 3
	if f := readFigures(t, s, run.ID); f != want {
		t.Fatalf("the run's counters keep growing but the frozen sample figures do not move: got %+v want %+v", f, want)
	}
}

func TestFailCountedAddsUsageAndCountsToTheirOwnColumns(t *testing.T) {
	ctx := context.Background()
	r, s, _, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 2)
	_ = r.Transition(ctx, run.ID, StateRunning, StatePlanned)
	b, _ := r.Next(ctx, run.ID, 2)
	if len(b.Items) != 2 {
		t.Fatalf("%+v", b)
	}
	if err := r.FailCounted(ctx, b.Items[0].ID, b.Items[0].Epoch, 3, 5, Usage{InputTokens: 11, OutputTokens: 2}); err != nil {
		t.Fatal(err)
	}
	if f := readFigures(t, s, run.ID); f.in != 11 || f.out != 2 {
		t.Fatalf("a failed attempt still cost tokens, and they must be recorded: got in=%d out=%d", f.in, f.out)
	}
	if f := readFigures(t, s, run.ID); f.acc != 3 || f.rej != 5 {
		t.Fatalf("accepted and rejected must land in their own columns, got accepted=%d rejected=%d", f.acc, f.rej)
	}
	if err := r.FailCounted(ctx, b.Items[1].ID, b.Items[1].Epoch, 1, 0, Usage{InputTokens: 6, OutputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	if f := readFigures(t, s, run.ID); f.in != 17 || f.out != 3 {
		t.Fatalf("usage adds up across failed attempts, got in=%d out=%d", f.in, f.out)
	}
	if f := readFigures(t, s, run.ID); f.acc != 4 || f.rej != 5 {
		t.Fatalf("counts add up across calls, got accepted=%d rejected=%d", f.acc, f.rej)
	}
}

// A run planned without a sample has nothing to measure. Starting its sample would settle at
// once with zero cost, a hollow figure for the go-ahead to rest on, so it is refused.
func TestARunPlannedWithoutASampleRefusesToStartOne(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 4)
	err := r.Transition(ctx, run.ID, StateSampling, StatePlanned)
	if !errors.Is(err, ErrState) {
		t.Fatalf("a run with a stored sample of 0 must not enter sampling: %v", err)
	}
	if got, _ := r.Get(ctx, run.ID); got.State != StatePlanned {
		t.Fatalf("a refused transition leaves the run planned, got %s", got.State)
	}
	if err := r.Transition(ctx, run.ID, StateRunning, StatePlanned); err != nil {
		t.Fatalf("a run planned without a sample can still run in full: %v", err)
	}
}
