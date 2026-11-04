package bootstrap

import (
	"fmt"
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
	digest := 20000 + headerBytes("internal/queue/queue.go") // a digest's bytes include its header lines
	wantIn := (4000/bytesPerToken + promptOverheadTokens) + (8000/bytesPerToken + promptOverheadTokens) + (digest/bytesPerToken + promptOverheadTokens)
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
	un := sampleResult()
	un.Sources[4].Hash = "other" // an uninspected component's file: in no work item, so only the source list covers it
	if BuildPlan(un, "repo", "abc123", 0).Hash == a.Hash {
		t.Fatal("a changed source in an uninspected component must change the plan hash")
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
	// Which branches and worktrees exist is local to one clone: another clone of the same commit
	// has others, or none. None of it may move the hash (final review A3).
	other := BuildPlan(withWorktrees(gitx.Worktree{Path: "/Users/a/repo-side", Branch: "other", Head: head}), "repo", "abc123", 0)
	if other.Hash != here.Hash {
		t.Fatal("a worktree on another branch is local to this clone and must not change the hash")
	}
	none := BuildPlan(sampleResult(), "repo", "abc123", 0)
	if none.Hash != here.Hash {
		t.Fatal("a clone with no other worktree must plan the same hash")
	}
	branched := sampleResult()
	branched.Sources = append(branched.Sources, UnreadSources("trunk", []gitx.Branch{{Name: "wip", Ahead: 4}}, nil)...)
	if BuildPlan(branched, "repo", "abc123", 0).Hash != here.Hash {
		t.Fatal("local branches, how far ahead they are and the default branch's name must not change the hash")
	}
}

// Unread records have a source name of their own, so a tracked root file literally named
// "worktree:main" is a different (name, path) from the record for a worktree on main.
func TestUnreadRecordsCannotCollideWithARepositoryFile(t *testing.T) {
	un := UnreadSources("main", []gitx.Branch{{Name: "x", Ahead: 1}}, []gitx.Worktree{{Path: "/w", Branch: "main"}})
	for _, s := range un {
		if s.Name != UnreadSourceName || s.Name == "repo" {
			t.Fatalf("an unread record is named %q, want %q", s.Name, UnreadSourceName)
		}
	}
}

func TestEstimateStagesVerifyTokensScaleWithInspectedComponents(t *testing.T) {
	r := sampleResult()
	r.Components[1].Inspected = true
	v := EstimateStages(r)[1]
	if v.Stage != "verify" || v.Items != 2 {
		t.Fatalf("verify items = %+v, want 2 (two inspected components)", v)
	}
	if v.InputTokens != 2*verifyInTokens || v.OutputTokens != 2*verifyOutTokens {
		t.Fatalf("verify tokens = %d/%d, want %d/%d (they scale with the item count)", v.InputTokens, v.OutputTokens, 2*verifyInTokens, 2*verifyOutTokens)
	}
}

func intentSources(n int) Result {
	var r Result
	for i := 0; i < n; i++ {
		r.Sources = append(r.Sources, Source{Name: "repo", Path: fmt.Sprintf("docs/%d.md", i), Tier: TierIntent, Disposition: DispQueued, Hash: fmt.Sprintf("h%d", i), Size: 100})
	}
	return r
}

func TestBatchCountRoundsUpAtTheBatchBoundary(t *testing.T) {
	for _, c := range []struct{ items, batches int }{{0, 0}, {BatchSize, 1}, {BatchSize + 1, 2}} {
		if got := BuildPlan(intentSources(c.items), "repo", "abc123", 0).Batches; got != c.batches {
			t.Fatalf("%d items give %d batches, want %d (batch size %d, rounded up)", c.items, got, c.batches, BatchSize)
		}
	}
}

func TestPlanHashCoversSourceTier(t *testing.T) {
	a := BuildPlan(sampleResult(), "repo", "abc123", 0)
	mut := sampleResult()
	mut.Sources[2].Tier = TierIntent // the skipped source: no work list or estimate changes
	b := BuildPlan(mut, "repo", "abc123", 0)
	if !slices.Equal(b.Estimates, a.Estimates) || b.Batches != a.Batches {
		t.Fatal("fixture error: the tier change must not move the estimates")
	}
	if b.Hash == a.Hash {
		t.Fatal("a changed source tier must change the plan hash: tiers decide what is extracted")
	}
}

func TestPlanHashCoversTheWorkListEstimatesAndBatches(t *testing.T) {
	p := BuildPlan(sampleResult(), "repo", "abc123", 0)

	work := p
	work.Result.Sources = slices.Clone(p.Sources)
	work.Result.Sources[0].Size++ // not an input to the hash; it changes only the work list
	if planHash(work) == p.Hash {
		t.Fatal("a changed work list must change the plan hash")
	}

	est := p
	est.Estimates = slices.Clone(p.Estimates)
	est.Estimates[0].InputTokens++ // what a changed estimate constant would do
	if planHash(est) == p.Hash {
		t.Fatal("changed estimates must change the plan hash")
	}

	bat := p
	bat.Batches++
	if planHash(bat) == p.Hash {
		t.Fatal("a changed batch count must change the plan hash")
	}

	if planHash(p) != p.Hash {
		t.Fatal("planHash must reproduce the hash BuildPlan stored")
	}
}
