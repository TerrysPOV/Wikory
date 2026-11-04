package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func coverageRig(t *testing.T) (*Runs, Run) {
	t.Helper()
	return coverageRigPlus(t, Source{Name: "nimbalyst", Path: "trackers/a.md", Tier: TierAttributed, Disposition: DispQueued, Hash: "hh", Size: 10},
		Source{Name: "repo", Path: "cfg.go", Tier: TierImplementation, Disposition: "skipped:secret", SecretSpans: 2, SecretLines: []int{4, 9}})
}

// coverageRigPlus is a run whose plan holds sampleResult plus the given sources.
func coverageRigPlus(t *testing.T, extra ...Source) (*Runs, Run) {
	t.Helper()
	ctx := context.Background()
	r, _, now, _ := newTestRuns(t)
	res := sampleResult()
	res.Sources = append(res.Sources, extra...)
	plan := BuildPlan(res, "repo+nimbalyst", "c0ffee0123456789c0ffee0123456789c0ffee01", 0)
	plan.Roots = map[string]string{"nimbalyst": "/home/me/proj/.nimbalyst"}
	run, err := r.Create(ctx, plan, specsN(2))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Transition(ctx, run.ID, StateRunning, StatePlanned)
	*now = now.Add(90 * time.Second)
	return r, run
}

func TestCoverageReportsReadSkippedSecretsAndRetirement(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	cov, err := BuildCoverage(ctx, r, run.ID, []PageOutcome{{Path: "p/a.md", Outcome: "written"}, {Path: "p/b.md", Outcome: "conflict"}}, r.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cov.Sources.Read != 5 || cov.Sources.Skipped["skipped:secret"] != 2 {
		t.Fatalf("read %d skipped %v", cov.Sources.Read, cov.Sources.Skipped)
	}
	if len(cov.SecretSkips) != 1 || cov.SecretSkips[0].Path != "cfg.go" || len(cov.SecretSkips[0].Lines) != 2 {
		t.Fatalf("secret skips name the file and the lines: %+v", cov.SecretSkips)
	}
	if cov.DurationSecs != 90 || cov.Pages[1].Outcome != "conflict" {
		t.Fatalf("%v %+v", cov.DurationSecs, cov.Pages)
	}
	if len(cov.Retirement) != 1 || !strings.Contains(cov.Retirement[0], "nimbalyst") || !strings.Contains(cov.Retirement[0], "/home/me/proj/.nimbalyst") || !strings.Contains(cov.Retirement[0], "delete") {
		t.Fatalf("one retirement entry per outside source, naming the directory: %v", cov.Retirement)
	}
	if !strings.Contains(cov.TokensNote, "asserted") {
		t.Fatal("token counts must be labelled asserted")
	}
}

func TestCoverageNeverContainsSecretTextOrClaimText(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	cov, _ := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	b, _ := json.Marshal(cov)
	if strings.Contains(string(b), "hunter2") {
		t.Fatal("no secret text")
	}
	for _, line := range strings.Split(cov.Text(), "\n") {
		if strings.Contains(line, "cfg.go") && !strings.Contains(line, "line") {
			t.Fatalf("a secret skip line should give line numbers: %q", line)
		}
	}
}

func TestCoverageClassifiesInspectedComponents(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	cov, _ := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if cov.Components.Total != 2 || cov.Components.Inspected != 1 || len(cov.Components.Unknown) != 1 || cov.Components.Unknown[0] != "internal/queue" {
		t.Fatalf("a component with no verified or conflicting claim is unknown: %+v", cov.Components)
	}
	if len(cov.Components.NotInspected) != 1 || cov.Components.NotInspected[0] != "internal/other" {
		t.Fatalf("%+v", cov.Components)
	}
}

// plantClaim stores a claim for a run directly, without going through a submission.
func plantClaim(t *testing.T, r *Runs, runID string, ordinal int, kind, status, text string) {
	t.Helper()
	var item string
	if err := r.DB.QueryRow(`SELECT item_id FROM bs_items WHERE run_id=? ORDER BY rowid LIMIT 1`, runID).Scan(&item); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DB.Exec(`INSERT INTO bs_claims(run_id,checkout_id,item_id,ordinal,claim_id,text,kind,status,meta,tier) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		runID, r.CheckoutID, item, ordinal, fmt.Sprintf("clm_%d", ordinal), text, kind, status, "", TierIntent); err != nil {
		t.Fatal(err)
	}
}

func TestCoverageNamesWhatWasNeverOpenedAndWhenSourcesLastChanged(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRigPlus(t,
		Source{Name: "nimbalyst", Path: "deep/er/", Tier: TierAttributed, Disposition: DispTooDeep},
		Source{Name: "nimbalyst", Path: "locked.md", Tier: TierAttributed, Disposition: DispUnreadable},
		Source{Name: "nimbalyst", Path: "old.md", Tier: TierAttributed, Disposition: DispQueued, Hash: "h5", Size: 3, Date: "2025-01-02T00:00:00Z"},
		Source{Name: "nimbalyst", Path: "new.md", Tier: TierAttributed, Disposition: DispQueued, Hash: "h6", Size: 3, Date: "2026-03-04T00:00:00Z"},
		Source{Name: UnreadSourceName, Path: "branch:feature/x (3 commit(s) not on main)", Tier: TierAttributed, Disposition: DispUnmergedBranch, Date: "2026-02-03T00:00:00Z"})
	cov, err := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, n := range cov.NotRead {
		got[n.Source+" "+n.Path] = n.Reason
	}
	want := map[string]string{
		"nimbalyst deep/er/": DispTooDeep, "nimbalyst locked.md": DispUnreadable,
		"git branch:feature/x (3 commit(s) not on main)": DispUnmergedBranch}
	if len(got) != len(want) {
		t.Fatalf("everything that was never opened is named, and nothing else: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%q: never opened for %q, want %q (%v)", k, got[k], v, got)
		}
	}
	if cov.Sources.Oldest != "2025-01-02T00:00:00Z" || cov.Sources.Newest != "2026-03-04T00:00:00Z" {
		t.Fatalf("the oldest and newest source dates come from the files that were read: %+v", cov.Sources)
	}
	text := cov.Text()
	for _, w := range []string{"not read (too-deep): nimbalyst deep/er/", "not read (unreadable): nimbalyst locked.md", "not read (unmerged-branch): git branch:feature/x"} {
		if !strings.Contains(text, w) {
			t.Fatalf("the text report must say %q:\n%s", w, text)
		}
	}
	if len(cov.Retirement) != 1 || !strings.Contains(cov.Retirement[0], "2 of the skipped were never opened") {
		t.Fatalf("a retirement checklist must say files were left behind before anything is deleted: %v", cov.Retirement)
	}
}

func TestCoverageCountsImplementationFilesThatWereScannedButNeverSummarised(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	cov, err := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cov.Sources.NotExtracted != 1 {
		t.Fatalf("internal/other/other.go sits in a component that was not checked: %+v", cov.Sources)
	}
	if !strings.Contains(cov.Text(), "scanned but not summarised") {
		t.Fatalf("%s", cov.Text())
	}
}

func TestCoverageCountsClaimsByKindAndStatus(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	plantClaim(t, r, run.ID, 1, "intent", "unverified", "One statement.")
	plantClaim(t, r, run.ID, 2, "intent", "historical", "Another statement.")
	plantClaim(t, r, run.ID, 3, "current-impl", "verified", "A third statement.")
	plantClaim(t, r, run.ID, 4, "current-impl", "verified", "A fourth statement.")
	cov, err := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if err != nil {
		t.Fatal(err)
	}
	if cov.Claims["verified"] != 2 || cov.ClaimsByKind["current-impl"]["verified"] != 2 || cov.ClaimsByKind["intent"]["historical"] != 1 || cov.ClaimsByKind["intent"]["unverified"] != 1 {
		t.Fatalf("claims by kind and status: %v / %v", cov.Claims, cov.ClaimsByKind)
	}
	text := cov.Text()
	if !strings.Contains(text, "current-impl: 2 (verified 2)") || !strings.Contains(text, "intent: 2 (historical 1, unverified 1)") {
		t.Fatalf("the text report gives counts by kind, then status:\n%s", text)
	}
	b, _ := json.Marshal(cov)
	if !strings.Contains(string(b), `"claims_by_kind"`) {
		t.Fatal("the JSON report carries claims_by_kind")
	}
}

func TestFinishSetsTheEndMarkerOnlyFromRunningOrFinishing(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	if err := r.Finish(ctx, run.ID, "c0ffee012345 2026-11-03T09:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(ctx, run.ID)
	if got.State != StateDone || got.EndMarker != "c0ffee012345 2026-11-03T09:00:00Z" {
		t.Fatalf("%+v", got)
	}
	if err := r.Finish(ctx, run.ID, "again"); err == nil {
		t.Fatal("a finished run cannot be finished again")
	}
	planned := mustRun(t, r, 0, 1)
	if err := r.Finish(ctx, planned.ID, "x"); err == nil {
		t.Fatal("a run that never started cannot finish")
	}
}
