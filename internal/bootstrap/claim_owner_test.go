package bootstrap

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// subpackageRig is a running run with two inspected components, internal/app and its
// sub-package internal/app/sub, each with an extract item for its digest.
func subpackageRig(t *testing.T) (*Runs, Run) {
	t.Helper()
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	res := Result{
		Sources: []Source{
			{Name: "repo", Path: "internal/app/app.go", Tier: TierImplementation, Disposition: DispQueued, Hash: "h1", Size: 10},
			{Name: "repo", Path: "internal/app/sub/x.go", Tier: TierImplementation, Disposition: DispQueued, Hash: "h2", Size: 10},
		},
		Components: []Component{
			{Name: "internal/app", Prefix: "internal/app", Rank: 1, Inspected: true, Files: 1},
			{Name: "internal/app/sub", Prefix: "internal/app/sub", Rank: 2, Inspected: true, Files: 1},
		},
	}
	run, err := r.Create(ctx, BuildPlan(res, "repo", "head1", 0), ExtractSpecs(res))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Transition(ctx, run.ID, StateRunning, StatePlanned); err != nil {
		t.Fatal(err)
	}
	return r, run
}

// plantOn stores a claim as if the extract item with the given ref had produced it.
func plantOn(t *testing.T, r *Runs, runID, ref, id, text, status string) {
	t.Helper()
	var item string
	if err := r.DB.QueryRow(`SELECT item_id FROM bs_items WHERE run_id=? AND ref=?`, runID, ref).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DB.Exec(`INSERT INTO bs_claims(run_id,checkout_id,item_id,ordinal,claim_id,text,kind,status,meta,tier) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		runID, r.CheckoutID, item, 1, id, text, "current-impl", status, "", TierImplementation); err != nil {
		t.Fatal(err)
	}
}

// A claim from internal/app/sub's digest naming sub's own file names internal/app too, as a
// substring. Its owner is sub: sub's verifier judged it, so the coverage report must credit sub
// alone, and the page it sits on must be sub's.
func TestTheVerifierThePageAndTheCoverageAgreeOnAClaimsComponent(t *testing.T) {
	ctx := context.Background()
	r, run := subpackageRig(t)
	plantOn(t, r, run.ID, "comp:internal/app/sub", "clm_s", "internal/app/sub/x.go retries twice", "verified")
	cov, err := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cov.Components.Verified, []string{"internal/app/sub"}) {
		t.Fatalf("only the component whose verifier judged the claim is verified: %+v", cov.Components)
	}
	if !slices.Equal(cov.Components.Unknown, []string{"internal/app"}) {
		t.Fatalf("internal/app has no claim of its own: %+v", cov.Components)
	}
	comps, err := r.Components(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := r.Claims(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := place(RenderModel{Components: comps, Claims: claims}).byComponent; len(got["internal/app"]) != 0 || len(got["internal/app/sub"]) != 1 {
		t.Fatalf("the claim sits on sub's page: %+v", got)
	}
	for _, c := range AssignClaims(comps, claims)["internal/app"] {
		if c.ClaimID == "clm_s" {
			t.Fatal("internal/app's verifier would be shown a claim sub owns")
		}
	}
}

// A claim from a component that is not inspected but naming one that is goes to the one that is,
// on the page as in the coverage report.
func TestAClaimPlacedByOwnershipReachesTheInspectedComponentItNames(t *testing.T) {
	comps := []Component{
		{Name: "internal/q", Prefix: "internal/q", Rank: 1, Inspected: true, Files: 1},
		{Name: "internal/idle", Prefix: "internal/idle", Rank: 2, Inspected: false, Files: 1},
	}
	cs := []StoredClaim{{ClaimID: "a", Kind: "current-impl", Status: "unverified", Ref: "comp:internal/idle", Text: "internal/q wakes the idle loop"}}
	pl := place(RenderModel{Components: comps, Claims: cs})
	if len(pl.byComponent["internal/q"]) != 1 || len(pl.general) != 0 || !strings.Contains(pl.byComponent["internal/q"][0].Text, "wakes") {
		t.Fatalf("the page agrees with the owner AssignClaims gives: %+v", pl)
	}
}

// Verification is queued for undecided work only: a component whose every claim is already
// decided gets no verify item, and no work file lists a decided claim.
func TestPrepareVerifyQueuesOnlyUndecidedWork(t *testing.T) {
	ctx := context.Background()
	r, run := subpackageRig(t)
	w, _ := newWork(t)
	plantOn(t, r, run.ID, "comp:internal/app/sub", "clm_s", "internal/app/sub/x.go retries twice", "verified")
	plantOn(t, r, run.ID, "comp:internal/app", "clm_a", "internal/app starts the server", "unverified")
	if _, err := r.DB.Exec(`UPDATE bs_items SET state='done' WHERE run_id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if owed, err := VerifyOwed(ctx, r, run.ID); err != nil || owed != 1 {
		t.Fatalf("VerifyOwed counts the component with undecided work only: %d %v", owed, err)
	}
	n, err := PrepareVerify(ctx, r, w, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("one verify item (internal/app); sub's only claim is decided: got %d", n)
	}
	var ref string
	if err := r.DB.QueryRow(`SELECT ref FROM bs_items WHERE run_id=? AND stage='verify'`, run.ID).Scan(&ref); err != nil || ref != "comp:internal/app" {
		t.Fatalf("verify item ref %q (%v)", ref, err)
	}
}
