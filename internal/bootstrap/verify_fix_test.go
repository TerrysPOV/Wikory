package bootstrap

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/ids"
)

// input builds a verify submission over the rig with an environment, blob reader and usage the
// test chooses.
func (v *verifyRig) input(env hookEnv, blob func(string, int64) ([]byte, error), verdicts ...Verdict) VerifyInput {
	return VerifyInput{Runs: v.r, Env: env, Mint: ids.NewULID, Blob: blob, Batch: v.batch,
		V: VerdictSet{Item: v.item.ID, Epoch: v.item.Epoch, Verdicts: verdicts, Usage: Usage{InputTokens: 5000, OutputTokens: 400}}}
}

func queueEnv() hookEnv {
	return hookEnv{fakeEnv: envWith("docs/decisions/0001.md", "internal/queue/queue.go")}
}

// requireUntouched fails unless the item is still leased with no attempt burned, the run's
// counters are as they were when the rig was built and both claims are exactly as the rig stored them.
func (v *verifyRig) requireUntouched(t *testing.T) {
	t.Helper()
	base := v.run0
	it, err := v.r.Item(context.Background(), v.item.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := v.r.Get(context.Background(), v.run.ID)
	if it.State != "leased" || it.Attempts != v.item.Attempts || run.TokensIn != base.TokensIn || run.Accepted != base.Accepted || run.Rejected != base.Rejected {
		t.Fatalf("the item must stay leased with nothing counted: %+v %+v", it, run)
	}
	for _, c := range []StoredClaim{v.c1, v.c2} {
		if got := claimByID(t, v.r, v.run.ID, c.ClaimID); got.Status != c.Status || got.Meta != c.Meta || got.Loc != c.Loc {
			t.Fatalf("a claim changed: %+v", got)
		}
	}
	if cs, _ := v.r.Claims(context.Background(), v.run.ID); len(cs) != 2 {
		t.Fatalf("no claim may be added: %+v", cs)
	}
}

func requireQuiet(t *testing.T, rep VerifyReport, item string) {
	t.Helper()
	if rep.Item != item || len(rep.Rejected) != 0 || rep.Rejected == nil || len(rep.Findings) != 0 || rep.Findings == nil ||
		rep.Confirmed != 0 || rep.Contradicted != 0 || rep.Undetermined != 0 || rep.Ignored != 0 || rep.Requeued {
		t.Fatalf("nothing was recorded, so the report must say nothing: %+v", rep)
	}
}

// A failure to read the plan's blob says nothing about the verdict. Treating it as a reject would
// spend a paid retry or leave the claim unjudged for the rest of the run.
func TestABlobReadFailureIsAnErrorNotAReject(t *testing.T) {
	v := newVerifyRig(t)
	boom := errors.New("git cat-file: broken pipe")
	rep, err := ApplyVerdicts(context.Background(), v.input(queueEnv(), func(string, int64) ([]byte, error) { return nil, boom },
		Verdict{Claim: v.c2.ClaimID, Result: "probably"}, // rejected before the blob is read
		Verdict{Claim: v.c1.ClaimID, Result: "confirmed", Evidence: codeLoc()}))
	if !errors.Is(err, boom) {
		t.Fatalf("a blob read failure must come back as an error, not a reject: %v %+v", err, rep)
	}
	requireQuiet(t, rep, v.item.ID)
	v.requireUntouched(t)
}

// A claim whose stored metadata no longer parses is corrupt store data, not the verifier's fault.
func TestACorruptedStoredClaimIsAnErrorNotAReject(t *testing.T) {
	v := newVerifyRig(t)
	if _, err := v.r.DB.Exec(`UPDATE bs_claims SET meta='not a claim comment' WHERE claim_id=?`, v.c1.ClaimID); err != nil {
		t.Fatal(err)
	}
	v.c1.Meta = "not a claim comment"
	rep, err := v.apply(t,
		Verdict{Claim: v.c2.ClaimID, Result: "probably"},
		Verdict{Claim: v.c1.ClaimID, Result: "confirmed", Evidence: codeLoc()})
	if err == nil || !strings.Contains(err.Error(), "no longer parses") {
		t.Fatalf("corrupt stored metadata must come back as an error, not a reject: %v %+v", err, rep)
	}
	requireQuiet(t, rep, v.item.ID)
	v.requireUntouched(t)
}

// markHistorical makes c1 a historical claim, with its metadata saying so.
func markHistorical(t *testing.T, v *verifyRig) {
	t.Helper()
	p, err := claim.ParseComment(v.c1.Meta)
	if err != nil {
		t.Fatal(err)
	}
	p.Status = "historical"
	meta := p.Render()
	if _, err := v.r.DB.Exec(`UPDATE bs_claims SET status='historical', meta=? WHERE claim_id=?`, meta, v.c1.ClaimID); err != nil {
		t.Fatal(err)
	}
	v.c1.Status, v.c1.Meta = "historical", meta
}

// The binary refused to verify on this evidence, so the verdict is not accepted: the claim is
// left exactly as it was. A historical claim in particular must not become unverified.
func TestARefusedConfirmationLeavesAHistoricalClaimByteIdentical(t *testing.T) {
	v := newVerifyRig(t)
	markHistorical(t, v)
	rep, err := ApplyVerdicts(context.Background(), v.input(hookEnv{fakeEnv: envWith("docs/decisions/0001.md")}, blobOf40, // queue.go is not committed
		Verdict{Claim: v.c1.ClaimID, Result: "confirmed", Evidence: codeLoc()}))
	if err != nil {
		t.Fatal(err)
	}
	got := claimByID(t, v.r, v.run.ID, v.c1.ClaimID)
	if got.Status != "historical" || got.Meta != v.c1.Meta || got.Loc != v.c1.Loc {
		t.Fatalf("a historical claim must stay exactly as it was: %+v", got)
	}
	if rep.Confirmed != 0 || rep.Undetermined != 2 {
		t.Fatalf("the refused confirmation counts as undetermined: %+v", rep)
	}
}

func TestARefusedConfirmationLeavesAnUnverifiedClaimByteIdentical(t *testing.T) {
	v := newVerifyRig(t)
	rep, err := ApplyVerdicts(context.Background(), v.input(hookEnv{fakeEnv: envWith("docs/decisions/0001.md")}, blobOf40,
		Verdict{Claim: v.c1.ClaimID, Result: "confirmed", Evidence: codeLoc()}))
	if err != nil {
		t.Fatal(err)
	}
	got := claimByID(t, v.r, v.run.ID, v.c1.ClaimID)
	if got.Status != "unverified" || got.Meta != v.c1.Meta || got.Loc != v.c1.Loc {
		t.Fatalf("an unverified claim must stay exactly as it was: %+v", got)
	}
	if rep.Confirmed != 0 || rep.Undetermined != 2 || len(rep.Findings) == 0 {
		t.Fatalf("counted as undetermined, with the validator's finding reported: %+v", rep)
	}
}

// Whether a claim is another component's must be decided before whether it is decided, or the
// error tells a verifier which ids exist and which are final, and an Ignored count stops the
// all-rejected requeue.
func TestAVerifierNamingAnotherComponentsDecidedClaimIsToldItIsUnknown(t *testing.T) {
	ctx := context.Background()
	r, w, run, c := sharedClaimRig(t)
	if _, err := PrepareVerify(ctx, r, w, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(ctx, run.ID, 5); err != nil { // the retry verifier owns c
		t.Fatal(err)
	}
	spec := ItemSpec{Stage: "verify", Ref: "comp:internal/queue", InputHash: ids.SHA256Hex([]byte("other"))}
	if _, err := r.enqueueVerify(ctx, run.ID, []ItemSpec{spec}); err != nil {
		t.Fatal(err)
	}
	ob, _ := r.Next(ctx, run.ID, 5)
	if len(ob.Items) != 1 {
		t.Fatalf("%+v", ob)
	}
	if _, err := r.DB.ExecContext(ctx, `UPDATE bs_claims SET status='verified' WHERE claim_id=?`, c.ClaimID); err != nil {
		t.Fatal(err)
	}
	rep, err := ApplyVerdicts(ctx, VerifyInput{Runs: r, Env: envWith("docs/decisions/0001.md", "internal/queue/queue.go"), Mint: ids.NewULID, Blob: blobOf40, Batch: ob.ID,
		V: VerdictSet{Item: ob.Items[0].ID, Epoch: ob.Items[0].Epoch, Verdicts: []Verdict{{Claim: c.ClaimID, Result: "confirmed", Evidence: codeLoc()}}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Ignored != 0 || len(rep.Rejected) != 1 || rep.Rejected[0].Code != "unknown-claim" || !rep.Requeued {
		t.Fatalf("a decided claim of another component must look like any claim that is not its own: %+v", rep)
	}
}

// failClaimInserts makes every later insert into bs_claims fail, so a completion that writes a
// new claim fails after its earlier writes and rolls them back.
func failClaimInserts(t *testing.T, r *Runs) {
	t.Helper()
	if _, err := r.DB.Exec(`CREATE TRIGGER fail_claim_inserts BEFORE INSERT ON bs_claims BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
}

// When the completion fails the transaction rolls back, so confirmations and contradictions that
// were counted while judging were never written and must not be reported.
func TestAFailedCompletionReportsNothingItDidNotWrite(t *testing.T) {
	v := newVerifyRig(t)
	failClaimInserts(t, v.r)
	rep, err := v.apply(t,
		Verdict{Claim: v.c2.ClaimID, Result: "probably"},
		Verdict{Claim: v.c1.ClaimID, Result: "contradicted", Evidence: codeLoc(), Corrected: "The internal/queue package acknowledges before the handler runs.", Quote: ackQuote})
	if err == nil || !strings.Contains(err.Error(), "disk full") || errors.Is(err, errClaimMoved) {
		t.Fatalf("the completion failure comes back unchanged: %v", err)
	}
	requireQuiet(t, rep, v.item.ID)
	v.requireUntouched(t)
}

// A claim that moves under a verdict puts the item back for a fresh look. The retry judges the
// claims again, so nothing counted in this attempt may be reported.
func TestAClaimMovedRequeueReportsNoRejectsIgnoredOrCounts(t *testing.T) {
	v := newVerifyRig(t)
	if _, err := v.r.DB.Exec(`UPDATE bs_claims SET status='verified' WHERE claim_id=?`, v.c2.ClaimID); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	env := hookEnv{fakeEnv: envWith("docs/decisions/0001.md", "internal/queue/queue.go"), onHead: func() {
		once.Do(func() {
			if _, err := v.r.DB.Exec(`UPDATE bs_claims SET status='verified' WHERE claim_id=?`, v.c1.ClaimID); err != nil {
				t.Error(err)
			}
		})
	}}
	rep, err := ApplyVerdicts(context.Background(), v.input(env, blobOf40,
		Verdict{Claim: v.c2.ClaimID, Result: "confirmed", Evidence: codeLoc()}, // already decided: ignored
		Verdict{Claim: "01NOSUCHCLAIM", Result: "not-determinable"},            // rejected
		Verdict{Claim: v.c1.ClaimID, Result: "confirmed", Evidence: codeLoc()}))
	if err != nil || !rep.Requeued || rep.Item != v.item.ID {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep.Ignored != 0 {
		t.Errorf("after a claim moved the report still carries %d ignored", rep.Ignored)
	}
	if len(rep.Rejected) != 0 || rep.Rejected == nil {
		t.Errorf("after a claim moved the report still carries rejects: %+v", rep.Rejected)
	}
	if rep.Confirmed != 0 || rep.Contradicted != 0 || rep.Undetermined != 0 || len(rep.Findings) != 0 || rep.Findings == nil {
		t.Errorf("after a claim moved the report still carries counts: %+v", rep)
	}
}

// ComponentFiles keeps only code, configuration and test files that were queued. Files inside the
// component's own directory that are documents or were skipped are not evidence.
func TestEvidenceInTheComponentsOwnDirectoryMustStillBeQueuedCode(t *testing.T) {
	res := sampleResult()
	res.Sources = append(res.Sources,
		Source{Name: "repo", Path: "internal/queue/README.md", Tier: TierAttributed, Disposition: DispQueued, Hash: "h6", Size: 300},
		Source{Name: "repo", Path: "internal/queue/skipped.go", Tier: TierImplementation, Disposition: "skipped:secret", Size: 300})
	for name, path := range map[string]string{
		"document in the component directory":     "internal/queue/README.md",
		"skipped file in the component directory": "internal/queue/skipped.go",
	} {
		v := newVerifyRigFor(t, res)
		rep, err := v.apply(t,
			Verdict{Claim: v.c1.ClaimID, Result: "confirmed", Evidence: []Locator{{Path: path, Lines: "L1-L2"}}},
			Verdict{Claim: v.c2.ClaimID, Result: "not-determinable"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(rep.Rejected) != 1 || rep.Rejected[0].Code != "bad-evidence-path" {
			t.Errorf("%s: rejected = %+v, want one bad-evidence-path", name, rep.Rejected)
		}
		if got := claimByID(t, v.r, v.run.ID, v.c1.ClaimID); got.Status != "unverified" || got.Meta != v.c1.Meta {
			t.Errorf("%s: a rejected verdict must change nothing: %+v", name, got)
		}
	}
}

// A contradiction is built from evidence the validator must be able to resolve against the
// committed files, as a confirmation is. Otherwise a verifier could plant a "conflicting" claim
// that points at nothing.
func TestAContradictionWithUnresolvableEvidenceIsRefusedAndWritesNothing(t *testing.T) {
	v := newVerifyRig(t)
	rep, err := ApplyVerdicts(context.Background(), v.input(hookEnv{fakeEnv: envWith("docs/decisions/0001.md")}, blobOf40, // queue.go is not committed
		Verdict{Claim: v.c1.ClaimID, Result: "contradicted", Evidence: codeLoc(), Corrected: "The internal/queue package acknowledges before the handler runs.", Quote: ackQuote},
		Verdict{Claim: v.c2.ClaimID, Result: "not-determinable"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rejected) != 1 || rep.Rejected[0].Code != "evidence-unresolved" || rep.Contradicted != 0 {
		t.Fatalf("a contradiction with unresolvable evidence must be refused: %+v", rep)
	}
	if got := claimByID(t, v.r, v.run.ID, v.c1.ClaimID); got.Status != "unverified" || got.Meta != v.c1.Meta {
		t.Fatalf("the original claim must be left alone: %+v", got)
	}
	if cs, _ := v.r.Claims(context.Background(), v.run.ID); len(cs) != 2 {
		t.Fatalf("no replacement claim may be stored: %+v", cs)
	}
}
