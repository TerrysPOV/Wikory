package bootstrap

import (
	"slices"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/gitx"
)

func sampleResult() Result {
	return Result{
		Sources: []Source{
			{Name: "repo", Path: "docs/decisions/0001.md", Tier: TierIntent, Disposition: DispQueued, Hash: "h1", Size: 4000},
			{Name: "repo", Path: "README.md", Tier: TierAttributed, Disposition: DispQueued, Hash: "h2", Size: 8000},
			{Name: "repo", Path: "secrets.md", Tier: TierAttributed, Disposition: "skipped:secret", Size: 100},
			{Name: "repo", Path: "internal/queue/queue.go", Tier: TierImplementation, Disposition: DispQueued, Hash: "h3", Size: 20000},
			{Name: "repo", Path: "internal/other/other.go", Tier: TierImplementation, Disposition: DispQueued, Hash: "h4", Size: 500},
		},
		Components: []Component{
			{Name: "internal/queue", Prefix: "internal/queue", Rank: 1, Inspected: true},
			{Name: "internal/other", Prefix: "internal/other", Rank: 2, Inspected: false},
		},
	}
}

func TestExtractSpecsOrderIntentThenAttributedThenInspectedComponents(t *testing.T) {
	var refs []string
	for _, s := range ExtractSpecs(sampleResult()) {
		refs = append(refs, s.Ref)
	}
	want := []string{"src:repo:docs/decisions/0001.md", "src:repo:README.md", "comp:internal/queue"}
	if !slices.Equal(refs, want) {
		t.Fatalf("refs = %v, want %v (skipped sources and uninspected components are not extracted)", refs, want)
	}
}

func TestComponentDigestSpecIsBoundedAndHashesItsFiles(t *testing.T) {
	r := sampleResult()
	r.Sources[3].Size = DigestMaxBytes * 3
	specs := ExtractSpecs(r)
	comp := specs[len(specs)-1]
	if comp.Bytes != DigestMaxBytes {
		t.Fatalf("a digest is capped at DigestMaxBytes, got %d", comp.Bytes)
	}
	before := comp.InputHash
	r.Sources[3].Hash = "changed"
	if after := ExtractSpecs(r); after[len(after)-1].InputHash == before {
		t.Fatal("a changed file must change the digest's input hash")
	}
}

func TestSampleSetIsSpreadAndExact(t *testing.T) {
	if len(SampleSet(0, 10)) != 0 {
		t.Fatal("no sample requested")
	}
	if got := SampleSet(20, 10); len(got) != 10 {
		t.Fatalf("a sample larger than the run is the whole run: %d", len(got))
	}
	got := SampleSet(3, 9)
	if len(got) != 3 || !got[0] || !got[3] || !got[6] {
		t.Fatalf("an even spread of 3 over 9 is 0,3,6: %v", got)
	}
}

func TestEstimateStagesCountsItemsAndTokens(t *testing.T) {
	est := EstimateStages(sampleResult())
	if len(est) != 2 || est[0].Stage != "extract" || est[1].Stage != "verify" {
		t.Fatalf("%+v", est)
	}
	if est[0].Items != 3 || est[1].Items != 1 {
		t.Fatalf("items: extract 3 (2 docs and 1 digest), verify 1 (one inspected component): %+v", est)
	}
	wantIn := (4000/bytesPerToken + promptOverheadTokens) + (8000/bytesPerToken + promptOverheadTokens) + (20000/bytesPerToken + promptOverheadTokens)
	if est[0].InputTokens != wantIn || est[0].OutputTokens != 3*extractOutPerItem {
		t.Fatalf("extract tokens = %d/%d, want %d/%d", est[0].InputTokens, est[0].OutputTokens, wantIn, 3*extractOutPerItem)
	}
}

func TestPlanHashIsStableAndSensitive(t *testing.T) {
	a := BuildPlan(sampleResult(), "repo", "abc123", 0)
	b := BuildPlan(sampleResult(), "repo", "abc123", 5)
	if a.Hash == "" || a.Hash != b.Hash {
		t.Fatalf("the hash is of the full plan, so --sample does not change it: %q vs %q", a.Hash, b.Hash)
	}
	mut := sampleResult()
	mut.Sources[1].Hash = "other"
	if BuildPlan(mut, "repo", "abc123", 0).Hash == a.Hash {
		t.Fatal("a changed source must change the plan hash")
	}
	if BuildPlan(sampleResult(), "repo", "def456", 0).Hash == a.Hash {
		t.Fatal("a different HEAD must change the plan hash")
	}
	if BuildPlan(sampleResult(), "repo+notes", "abc123", 0).Hash == a.Hash {
		t.Fatal("a different scope must change the plan hash")
	}
	if a.Batches != 1+1 {
		t.Fatalf("batches = %d, want 2 (one extract batch of up to %d, one verify batch)", a.Batches, BatchSize)
	}
}

func withWorktrees(trees ...gitx.Worktree) Result {
	r := sampleResult()
	r.Sources = append(r.Sources, UnreadSources("main", nil, trees)...)
	return r
}

func TestPlanHashDoesNotDependOnWhereAWorktreeLives(t *testing.T) {
	head := "0123456789abcdef0123456789abcdef01234567"
	here := BuildPlan(withWorktrees(gitx.Worktree{Path: "/Users/a/repo-side", Branch: "side", Head: head}), "repo", "abc123", 0)
	there := BuildPlan(withWorktrees(gitx.Worktree{Path: "/srv/ci/moved/elsewhere", Branch: "side", Head: head}), "repo", "abc123", 0)
	if here.Hash != there.Hash {
		t.Fatalf("the same repository state must hash the same in another directory or on another machine: %s vs %s", here.Hash, there.Hash)
	}
	other := BuildPlan(withWorktrees(gitx.Worktree{Path: "/Users/a/repo-side", Branch: "other", Head: head}), "repo", "abc123", 0)
	if other.Hash == here.Hash {
		t.Fatal("a different branch checked out elsewhere is a different repository state, so it must change the hash")
	}
	none := BuildPlan(sampleResult(), "repo", "abc123", 0)
	if none.Hash == here.Hash {
		t.Fatal("having another worktree at all must change the hash")
	}
}
