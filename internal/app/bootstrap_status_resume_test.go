package app

import (
	"strings"
	"testing"
)

// Step 0 of the command decides how to resume from `wikory bootstrap status --json` alone: the
// latest run's state, plan hash, id and sample size, and the sample's measurements. With no run
// at all, status says so and the command plans.
func TestStatusJSONGivesTheCommandWhatItNeedsToResume(t *testing.T) {
	dir, c := bootstrapRepo(t)
	if e, code, _ := runBootstrapJSON(t, c, dir, "status"); code == ExitOK || e.OK || !strings.Contains(e.Error.Message, "no such run") {
		t.Fatalf("with no run, status says there is none: %d %+v", code, e)
	}
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[planData](t, pe)
	if e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash, "--sample"); code != ExitOK || !e.OK {
		t.Fatalf("confirm --sample: %d %+v", code, e)
	}
	st, code, _ := runBootstrapJSON(t, c, dir, "status")
	got := decodeData[struct {
		Run struct {
			ID       string `json:"id"`
			State    string `json:"state"`
			PlanHash string `json:"plan_hash"`
			Sample   int    `json:"sample"`
		} `json:"run"`
		Sample *struct {
			Items int `json:"sample_items"`
			Done  int `json:"sample_done"`
		} `json:"sample"`
	}](t, st)
	if code != ExitOK || got.Run.ID != d.RunID || got.Run.State != "sampling" || got.Run.PlanHash != d.PlanHash || got.Run.Sample != 1 {
		t.Fatalf("status --json names the latest run, its state, plan hash and sample size: %d %+v", code, got)
	}
	if got.Sample == nil || got.Sample.Items != 1 || got.Sample.Done != 0 {
		t.Fatalf("status --json gives the sample's measurements: %+v", got.Sample)
	}
}
