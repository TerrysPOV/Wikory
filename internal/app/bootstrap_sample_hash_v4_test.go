package app

import (
	"strings"
	"testing"
)

// Fix wave 3 (2026-11-10): the sample size is in the plan hash. A hash shown for one sample size
// cannot start a sample of another, and a run whose stored sample size was changed no longer
// rebuilds the hash it was planned under, so it cannot be confirmed at all.
func TestASampleStartsOnlyUnderTheHashOfItsOwnSampleSize(t *testing.T) {
	dir, c := bootstrapRepo(t)
	one := decodeData[planData](t, first(runBootstrapJSON(t, c, dir, "plan", "--sample", "1")))
	two := decodeData[planData](t, first(runBootstrapJSON(t, c, dir, "plan", "--sample", "2")))
	if one.PlanHash == two.PlanHash {
		t.Fatalf("plans of different sample sizes must have different hashes: %s", one.PlanHash)
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", one.RunID, "--plan-hash", two.PlanHash, "--sample")
	if code != ExitUsage || e.Error == nil || !strings.Contains(e.Error.Message, "not this plan's hash") {
		t.Fatalf("a hash made with another sample size must be refused: %d %+v", code, e)
	}
	// The stored sample size is changed behind the plan: the rebuilt hash no longer matches.
	execDB(t, c, `UPDATE bs_runs SET sample=5 WHERE run_id=?`, two.RunID)
	e, code, _ = runBootstrapJSON(t, c, dir, "confirm", "--run", two.RunID, "--plan-hash", two.PlanHash, "--sample")
	if code != ExitUsage || e.Error == nil || !strings.Contains(e.Error.Message, "no longer matches the plan that was shown") {
		t.Fatalf("a run whose sample size changed after the plan must be refused: %d %+v", code, e)
	}
	if e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", one.RunID, "--plan-hash", one.PlanHash, "--sample"); code != ExitOK || !e.OK {
		t.Fatalf("the hash shown for the run's own sample size starts it: %d %+v", code, e)
	}
}

func first(e Envelope, _ int, _ string) Envelope { return e }
