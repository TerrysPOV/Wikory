package bootstrap

import (
	"context"
	"testing"
)

// A component with no file to read is ranked but not inspected, and the budget passes to the
// next one, so inspection is spent where there is something to read. The rule is deterministic:
// it depends only on Files, which the inventory derives from the sources.
func TestRankPassesTheBudgetOverAComponentWithNoFiles(t *testing.T) {
	got := Rank([]Component{
		{Name: "loud", Prefix: "loud", Churn: 100},
		{Name: "quiet", Prefix: "quiet", Churn: 1, Files: 2},
		{Name: "last", Prefix: "last", Files: 1},
	}, 1)
	if got[0].Name != "loud" || got[0].Rank != 1 || got[0].Inspected {
		t.Fatalf("an empty component keeps its rank but is not inspected: %+v", got[0])
	}
	if got[1].Name != "quiet" || !got[1].Inspected || got[2].Inspected {
		t.Fatalf("the budget of one goes to the next component that has files: %+v", got)
	}
}

// End to end through the inventory: a component whose only file is skipped for a secret has no
// files, so it gets no extract item and the next component is inspected instead.
func TestInventorySpendsNoPaidWorkOnAComponentWhoseFilesWereAllSkipped(t *testing.T) {
	ctx := context.Background()
	repo := &memReader{name: "repo", files: map[string]string{
		"internal/secretive/s.go": "package secretive\n\nconst DB_PASS" + "WORD = \"hunter2" + "hunter2\"\n",
		"internal/plain/p.go":     "package plain\n\nfunc P() {}\n",
	}}
	res, err := Inventory(ctx, []Reader{repo}, Options{MaxFileBytes: MaxFileBytes, Budget: 1,
		Churn: map[string]int{"internal/secretive/s.go": 40, "internal/plain/p.go": 1}})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Component{}
	for _, c := range res.Components {
		by[c.Prefix] = c
	}
	if s := by["internal/secretive"]; s.Files != 0 || s.Inspected {
		t.Fatalf("a component whose every file was skipped has no files and is not inspected: %+v", s)
	}
	if !by["internal/plain"].Inspected {
		t.Fatalf("the budget goes to the next component: %+v", res.Components)
	}
	for _, sp := range ExtractSpecs(res) {
		if sp.Ref == "comp:internal/secretive" {
			t.Fatal("a component with no files got a paid extract item")
		}
	}
}

// Whatever marks a component inspected, one with no files gets neither an extract item nor a
// verify target: its digest would be empty and every verdict on its claims would cite no file.
func TestAnInspectedComponentWithNoFilesGetsNoExtractItemAndNoVerifyTarget(t *testing.T) {
	res := Result{
		Sources: []Source{{Name: "repo", Path: "internal/full/f.go", Tier: TierImplementation, Disposition: DispQueued, Hash: "h", Size: 10}},
		Components: []Component{
			{Name: "internal/empty", Prefix: "internal/empty", Rank: 1, Inspected: true},
			{Name: "internal/full", Prefix: "internal/full", Rank: 2, Inspected: true, Files: 1},
		},
	}
	for _, sp := range ExtractSpecs(res) {
		if sp.Ref == "comp:internal/empty" {
			t.Fatal("an inspected component with no files got an extract item")
		}
	}
	if v := EstimateStages(res)[1]; v.Items != 1 {
		t.Fatalf("only the component with files is estimated for verification: %+v", v)
	}
	assigned := map[string][]StoredClaim{
		"internal/empty": {{ClaimID: "clm_a", Ref: "comp:internal/empty"}},
		"internal/full":  {{ClaimID: "clm_b", Ref: "comp:internal/full"}},
	}
	got := verifyTargets(res.Components, assigned, ComponentFiles(res))
	if len(got) != 1 || got[0].Prefix != "internal/full" {
		t.Fatalf("only the component with files is a verify target: %+v", got)
	}
}
