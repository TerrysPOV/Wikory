package app

import "testing"

// Starting a sample spends money too, so `confirm --sample` needs the exact plan hash just as the
// full confirm does: a wrong or missing hash leaves the run planned and hands out nothing.
func TestConfirmSampleNeedsTheExactPlanHash(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[planData](t, pe)
	for _, bad := range [][]string{
		{"confirm", "--run", d.RunID, "--sample"},
		{"confirm", "--run", d.RunID, "--plan-hash", "deadbeef", "--sample"},
	} {
		if e, code, _ := runBootstrapJSON(t, c, dir, bad...); code != ExitUsage || e.OK {
			t.Errorf("%v must be refused: %d %+v", bad, code, e)
		}
	}
	if got := stateOf(t, c, dir, d.RunID); got != "planned" {
		t.Fatalf("a refused sample confirm leaves the run planned, got %q", got)
	}
	if e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash, "--sample"); code != ExitOK || !e.OK {
		t.Fatalf("the right hash starts the sample: %d %+v", code, e)
	}
}
