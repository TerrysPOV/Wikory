package bootstrap

import (
	"context"
	"reflect"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/ids"
)

// sampleRig is a run planned with a sample of one of its two extract items: the design doc,
// whose two claims name internal/queue, an inspected component. The sample is started and its
// one extract item submitted, so extraction is closed for the sample and the other extract item
// is still pending for the full run.
type sampleRig struct {
	r   *Runs
	w   WorkFiles
	run Run
}

func newSampleRig(t *testing.T) *sampleRig {
	t.Helper()
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	w, _ := newWork(t)
	hash := ids.SHA256Hex([]byte(workText))
	specs := []ItemSpec{
		{Stage: "extract", Ref: "src:repo:docs/decisions/0001.md", InputHash: hash},
		{Stage: "extract", Ref: "src:repo:docs/decisions/0002.md", InputHash: "unsampled"},
	}
	run, err := r.Create(ctx, BuildPlan(sampleResult(), "repo", "head1", 1), specs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(hash, workText); err != nil {
		t.Fatal(err)
	}
	if err := r.Transition(ctx, run.ID, StateSampling, StatePlanned); err != nil {
		t.Fatal(err)
	}
	b, err := r.Next(ctx, run.ID, 5)
	if err != nil || len(b.Items) != 1 || b.Items[0].Ref != specs[0].Ref {
		t.Fatalf("the sample leases its one extract item: %+v %v", b, err)
	}
	prop := Proposal{Item: b.Items[0].ID, Epoch: 1, Usage: Usage{InputTokens: 1000, OutputTokens: 100}, Claims: []ProposedClaim{
		{Text: "The internal/queue package acknowledges after the handler returns.", Kind: "current-impl", Excerpt: "acknowledged only after the handler returns", Locator: "L3-L3"},
		{Text: "Retries in internal/queue stop after five attempts.", Kind: "current-impl", Excerpt: "Retries stop after five attempts.", Locator: "L6-L6"},
	}}
	if _, err := Submit(ctx, SubmitInput{Runs: r, Env: envWith("docs/decisions/0001.md"), Work: w, Mint: ids.NewULID, Batch: b.ID, P: prop}); err != nil {
		t.Fatal(err)
	}
	return &sampleRig{r: r, w: w, run: run}
}

func (s *sampleRig) state(t *testing.T) string {
	t.Helper()
	got, err := s.r.Get(context.Background(), s.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got.State
}

// Terry's ruling (2026-11-10): the sample runs the paid pipeline, extract and verify, on its
// items. A sample whose extraction yielded claims waits for their verification before it settles,
// and Next alone (which cannot prepare work files) must not settle it early.
func TestASampleVerifiesItsClaimsBeforeItSettles(t *testing.T) {
	ctx := context.Background()
	s := newSampleRig(t)
	if got := s.state(t); got != StateSampling {
		t.Fatalf("a sample whose claims are not yet verified must not settle, got %s", got)
	}
	if b, err := s.r.Next(ctx, s.run.ID, 5); err != nil || len(b.Items) != 0 || s.state(t) != StateSampling {
		t.Fatalf("Next before the verify work is prepared hands out nothing and settles nothing: %+v %v %s", b, err, s.state(t))
	}
	n, err := PrepareVerify(ctx, s.r, s.w, s.run.ID)
	if err != nil || n != 1 {
		t.Fatalf("the sample's claims get one verify item for their component: %d %v", n, err)
	}
	vb, err := s.r.Next(ctx, s.run.ID, 5)
	if err != nil || len(vb.Items) != 1 || vb.Items[0].Stage != "verify" || vb.Items[0].Ref != "sample:comp:internal/queue" {
		t.Fatalf("the sample hands out its verify item: %+v %v", vb, err)
	}
	if again, err := s.r.Next(ctx, s.run.ID, 5); err != nil || len(again.Items) != 0 || s.state(t) != StateSampling {
		t.Fatalf("a sample whose verify item is leased must not settle when Next finds nothing more: %+v %v %s", again, err, s.state(t))
	}
	cs, _ := s.r.Claims(ctx, s.run.ID)
	ack := cs[0]
	if ack.Text[:4] != "The " {
		ack = cs[1]
	}
	rep, err := ApplyVerdicts(ctx, VerifyInput{Runs: s.r, Env: envWith("docs/decisions/0001.md", "internal/queue/queue.go"), Mint: ids.NewULID, Blob: blobOf40, Batch: vb.ID,
		V: VerdictSet{Item: vb.Items[0].ID, Epoch: vb.Items[0].Epoch, Usage: Usage{InputTokens: 5000, OutputTokens: 400},
			Verdicts: []Verdict{{Claim: ack.ClaimID, Result: "contradicted", Evidence: codeLoc(), Corrected: "The internal/queue package acknowledges before the handler runs.", Quote: ackQuote}}}})
	if err != nil || rep.Contradicted != 1 || rep.Undetermined != 1 {
		t.Fatalf("the sample's verdicts are applied like the full run's: %+v %v", rep, err)
	}
	if got := s.state(t); got != StateAwaiting {
		t.Fatalf("the sample settles once its verify item is done, got %s", got)
	}
	m, err := s.r.Measure(ctx, s.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.SampleItems != 1 || m.SampleDone != 1 || m.VerifyItems != 1 || m.VerifyDone != 1 ||
		m.ExtractTokens != 1100 || m.VerifyTokens != 5400 || m.TokensIn != 6000 || m.TokensOut != 500 ||
		m.Verdicts != (Verdicts{Confirmed: 0, Contradicted: 1, Undetermined: 1}) {
		t.Fatalf("the measurement covers both stages of the sample: %+v", m)
	}
	// The plan has one inspected component with files, so the full run's verification is
	// projected as one more verify item at what the sample's cost.
	if m.ProjectedVerifyTokens == nil || *m.ProjectedVerifyTokens != 5400 {
		t.Fatalf("projected verification = %v, want 5400", m.ProjectedVerifyTokens)
	}
	if m.ProjectedTokens == nil || *m.ProjectedTokens != 1100 {
		t.Fatalf("projected extraction for the one item left = %v, want 1100", m.ProjectedTokens)
	}

	// The full run verifies what the sample left undecided, under the full run's own item.
	if err := s.r.Transition(ctx, s.run.ID, StateRunning, StateAwaiting); err != nil {
		t.Fatal(err)
	}
	if n, err := PrepareVerify(ctx, s.r, s.w, s.run.ID); err != nil || n != 0 {
		t.Fatalf("no full-run verify item while the full run's extraction is open: %d %v", n, err)
	}
	rest, _ := s.r.Next(ctx, s.run.ID, 5)
	if len(rest.Items) != 1 || rest.Items[0].Stage != "extract" {
		t.Fatalf("the full run leases the unsampled extract item: %+v", rest)
	}
	if err := s.r.Complete(ctx, rest.Items[0].ID, rest.Items[0].Epoch, Usage{}, nil); err != nil {
		t.Fatal(err)
	}
	if owed, err := VerifyOwed(ctx, s.r, s.run.ID); err != nil || owed != 1 {
		t.Fatalf("the sample's verify item does not count as the full run's: owed %d %v", owed, err)
	}
	if n, err := PrepareVerify(ctx, s.r, s.w, s.run.ID); err != nil || n != 1 {
		t.Fatalf("the full run queues its own verify item for the claim the sample left undecided: %d %v", n, err)
	}
	if owed, err := VerifyOwed(ctx, s.r, s.run.ID); err != nil || owed != 0 {
		t.Fatalf("nothing is owed once the full run's verify item is queued: %d %v", owed, err)
	}
	after, _ := s.r.Measure(ctx, s.run.ID)
	if !reflect.DeepEqual(after, m) {
		t.Fatalf("the sample's figures stay frozen: before %+v after %+v", m, after)
	}
}

// When nothing the sample extracted can be verified (no claim touches an inspected component),
// preparing the verify stage settles the sample at once: there is no verify work to wait for.
func TestASampleWithNothingToVerifySettlesWhenVerifyIsPrepared(t *testing.T) {
	ctx := context.Background()
	s := newSampleRig(t)
	// The claims stop naming the component, so no verifier owns them.
	if _, err := s.r.DB.Exec(`UPDATE bs_claims SET text=replace(text, 'internal/queue', 'the queue') WHERE run_id=?`, s.run.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := PrepareVerify(ctx, s.r, s.w, s.run.ID); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	if got := s.state(t); got != StateAwaiting {
		t.Fatalf("a sample with nothing to verify settles when verification is prepared, got %s", got)
	}
	if m, _ := s.r.Measure(ctx, s.run.ID); m.VerifyItems != 0 || m.ProjectedVerifyTokens != nil {
		t.Fatalf("no verify item ran, so the full run's verification cannot be projected: %+v", m)
	}
}

// The sample's verify stage waits for the sample's own extraction only: the full run's pending
// extract items do not hold it back, and a sampled extract item still open does.
func TestASampleVerifiesOnceItsOwnExtractionIsClosed(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	w, _ := newWork(t)
	run := mustRun(t, r, 1, 3)
	_ = r.Transition(ctx, run.ID, StateSampling, StatePlanned)
	b, _ := r.Next(ctx, run.ID, 5)
	if n, err := PrepareVerify(ctx, r, w, run.ID); err != nil || n != 0 || len(b.Items) != 1 {
		t.Fatalf("nothing is prepared while the sampled extract item is leased: %d %v", n, err)
	}
	if got, _ := r.Get(ctx, run.ID); got.State != StateSampling {
		t.Fatalf("a sample with a leased extract item must not settle, got %s", got.State)
	}
	if err := r.Complete(ctx, b.Items[0].ID, b.Items[0].Epoch, Usage{}, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Get(ctx, run.ID); got.State != StateAwaiting {
		t.Fatalf("a sampled item with no claims leaves nothing to verify, so the sample settles, got %s", got.State)
	}
}
