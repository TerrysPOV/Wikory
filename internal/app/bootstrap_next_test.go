package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapNextRefusesAnUnconfirmedRunAndStatusReportsIt(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	run := decodeData[planData](t, pe).RunID
	e, code, _ := runBootstrapJSON(t, c, dir, "next", "--run", run)
	if code != ExitFailure || e.OK || !strings.Contains(e.Error.Message, "planned") {
		t.Fatalf("an unconfirmed run must not hand out work: code=%d %+v", code, e)
	}
	st, code, _ := runBootstrapJSON(t, c, dir, "status", "--run", run)
	if code != ExitOK {
		t.Fatalf("%+v", st)
	}
	got := decodeData[struct {
		Run struct {
			State string `json:"state"`
		} `json:"run"`
		Items map[string]map[string]int `json:"items"`
	}](t, st)
	if got.Run.State != "planned" || got.Items["extract"]["pending"] < 2 {
		t.Fatalf("a planned run holds its extract items as pending: %+v", got.Items)
	}
}

func TestBootstrapStatusWithoutARunNamesTheLatestAndNextNeedsRun(t *testing.T) {
	dir, c := bootstrapRepo(t)
	if _, code, _ := runBootstrapJSON(t, c, dir, "status"); code == ExitOK {
		t.Fatal("no runs yet: status must say so, not invent one")
	}
	_, _, _ = runBootstrapJSON(t, c, dir, "plan")
	if _, code, _ := runBootstrapJSON(t, c, dir, "status"); code != ExitOK {
		t.Fatal("status with no --run reads the latest run")
	}
	if _, code, _ := runBootstrapJSON(t, c, dir, "next"); code != ExitUsage {
		t.Fatalf("next without --run is a usage error, got %d", code)
	}
}

// The sample size is not in the plan hash, so the run itself must carry it: two plans of the same
// repository that differ only in --sample have the same hash and different runs.
func TestBootstrapPlanBindsTheSampleSizeToTheRun(t *testing.T) {
	dir, c := bootstrapRepo(t)
	type statusRun struct {
		Run struct {
			State    string `json:"state"`
			Sample   int    `json:"sample"`
			PlanHash string `json:"plan_hash"`
		} `json:"run"`
	}
	full, _, _ := runBootstrapJSON(t, c, dir, "plan")
	sampled, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "2")
	df, ds := decodeData[planData](t, full), decodeData[planData](t, sampled)
	if df.PlanHash != ds.PlanHash {
		t.Fatalf("precondition: --sample is outside the plan hash: %s %s", df.PlanHash, ds.PlanHash)
	}
	for _, tc := range []struct {
		d    planData
		want int
	}{{df, 0}, {ds, 2}} {
		st, code, _ := runBootstrapJSON(t, c, dir, "status", "--run", tc.d.RunID)
		got := decodeData[statusRun](t, st)
		if code != ExitOK || got.Run.Sample != tc.want || tc.d.Sample != tc.want || got.Run.PlanHash != tc.d.PlanHash {
			t.Fatalf("the run must be bound to the sample it was planned with: want %d, run %+v, report %d", tc.want, got.Run, tc.d.Sample)
		}
	}
}

func TestBootstrapNextHandsOutRedactedWorkFilesAndNeverTheSameItemTwice(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	run := decodeData[planData](t, pe).RunID
	forceState(t, c, run, "running")
	e, code, _ := runBootstrapJSON(t, c, dir, "next", "--run", run, "--max", "50")
	if code != ExitOK {
		t.Fatalf("%+v", e)
	}
	d := decodeData[struct {
		Items []struct {
			Item     string `json:"item"`
			Ref      string `json:"ref"`
			Epoch    int    `json:"epoch"`
			WorkPath string `json:"work_path"`
		} `json:"items"`
	}](t, e)
	if len(d.Items) < 2 {
		t.Fatalf("expected the design doc, the handoff and a component digest: %+v", d.Items)
	}
	sawComp := false
	for _, it := range d.Items {
		b := readFile(t, it.WorkPath)
		if it.Epoch != 1 || b == "" {
			t.Fatalf("%+v", it)
		}
		if strings.HasPrefix(it.Ref, "comp:") {
			sawComp = true
			if !strings.Contains(b, "=== wikory-file internal/queue/queue.go ") {
				t.Fatalf("digest: %s", b)
			}
		}
	}
	if !sawComp {
		t.Fatal("an inspected component should be offered as a digest item")
	}
	again, _, _ := runBootstrapJSON(t, c, dir, "next", "--run", run, "--max", "50")
	if n := len(decodeData[struct {
		Items []struct{} `json:"items"`
	}](t, again).Items); n != 0 {
		t.Fatalf("leased items must not be handed out again: %d", n)
	}
}

// If the store lost a work file between plan and next, next says which item and refuses; it
// does not hand out a path to nothing, and it prints no file text.
func TestBootstrapNextNamesAnItemWhoseWorkFileIsMissing(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	run := decodeData[planData](t, pe).RunID
	forceState(t, c, run, "running")
	removed := 0
	_ = filepath.WalkDir(c.dataDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(filepath.Dir(p)) == "work" && strings.HasSuffix(p, ".txt") {
			if os.Remove(p) == nil {
				removed++
			}
		}
		return nil
	})
	if removed < 2 {
		t.Fatalf("precondition: the plan should have written work files to remove, removed %d", removed)
	}
	out, errOut, code := c.run(dir, "--json", "bootstrap", "next", "--run", run, "--max", "50")
	if code == ExitOK {
		t.Fatalf("a missing work file must fail the call: %s", out)
	}
	all := out + errOut
	if !strings.Contains(all, "the work file for ") || !strings.Contains(all, " is missing") || !strings.Contains(all, "src:") && !strings.Contains(all, "comp:") {
		t.Fatalf("the error must name the item whose work file is missing: %s", all)
	}
	if strings.Contains(all, "acknowledges after the handler") || strings.Contains(all, "package queue") {
		t.Fatalf("no file text may be printed: %s", all)
	}
}
