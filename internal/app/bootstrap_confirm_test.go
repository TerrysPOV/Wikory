package app

import (
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfirmNeedsTheExactPlanHash(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	d := decodeData[planData](t, pe)
	for _, bad := range [][]string{
		{"confirm", "--run", d.RunID},
		{"confirm", "--run", d.RunID, "--plan-hash", "deadbeef"},
		{"confirm", "--run", d.RunID, "--plan-hash", strings.ToUpper(d.PlanHash)},
	} {
		e, code, _ := runBootstrapJSON(t, c, dir, bad...)
		if code != ExitUsage || e.OK {
			t.Errorf("%v: code %d %+v", bad, code, e)
		}
	}
	if _, code, _ := runBootstrapJSON(t, c, dir, "next", "--run", d.RunID); code == ExitOK {
		t.Fatal("a run no one confirmed must not hand out work")
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash)
	if code != ExitOK || !e.OK {
		t.Fatalf("%d %+v", code, e)
	}
	if _, code, _ := runBootstrapJSON(t, c, dir, "next", "--run", d.RunID); code != ExitOK {
		t.Fatal("a confirmed run hands out work")
	}
}

func TestASampledRunNeedsTheSampleConfirmationFirst(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[planData](t, pe)
	if d.Sample != 1 {
		t.Fatalf("%+v", d)
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash)
	if code != ExitUsage || !strings.Contains(e.Error.Message, "--sample") {
		t.Fatalf("a run planned with a sample cannot jump to the full run: %d %+v", code, e)
	}
	if _, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash, "--sample"); code != ExitOK {
		t.Fatal("confirm --sample")
	}
	ne, _, _ := runBootstrapJSON(t, c, dir, "next", "--run", d.RunID, "--max", "50")
	items := decodeData[struct {
		Items []struct {
			Item  string `json:"item"`
			Epoch int    `json:"epoch"`
		} `json:"items"`
	}](t, ne).Items
	if len(items) != 1 {
		t.Fatalf("the sample is exactly the one item asked for: %d", len(items))
	}
	if _, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash); code == ExitOK {
		t.Fatal("the full run cannot start while the sample is still running")
	}
}

func TestConfirmSampleOnARunWithoutASampleIsAUsageError(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	d := decodeData[planData](t, pe)
	if _, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash, "--sample"); code != ExitUsage {
		t.Fatal("--sample needs a run planned with --sample N")
	}
}

func TestStatusShowsTheMeasuredSampleAndLabelsTokensAsAsserted(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[planData](t, pe)
	_, _, _ = runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash, "--sample")
	out, _, code := c.run(dir, "bootstrap", "status", "--run", d.RunID)
	if code != ExitOK || !strings.Contains(out, "asserted") || !strings.Contains(out, "sample") {
		t.Fatalf("%s", out)
	}
	if !strings.Contains(out, "extraction only") || !strings.Contains(out, "verification is not sampled") {
		t.Fatalf("the status must say the sample covers extraction only: %s", out)
	}
}

// execDB runs a statement against the checkout's database, as a damaged or tampered store would look.
func execDB(t *testing.T, c *cli, query string, args ...any) int64 {
	t.Helper()
	var dbPath string
	_ = filepath.WalkDir(c.dataDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "wikory.db" {
			dbPath = p
		}
		return nil
	})
	if dbPath == "" {
		t.Fatal("no wikory.db under the data directory")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := res.RowsAffected()
	return n
}

// A refused confirm must leave the run unconfirmed, whichever check refused it.
func assertNoWorkHandedOut(t *testing.T, c *cli, dir, runID, why string) {
	t.Helper()
	if _, code, _ := runBootstrapJSON(t, c, dir, "next", "--run", runID); code == ExitOK {
		t.Fatalf("%s: the run must not hand out work", why)
	}
}

func TestConfirmRefusesARunWhoseRepositoryHasMoved(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[planData](t, pe)
	gitIn(t, dir, "commit", "--allow-empty", "-qm", "the repository moves on")
	for _, args := range [][]string{
		{"confirm", "--run", d.RunID, "--plan-hash", d.PlanHash, "--sample"},
		{"confirm", "--run", d.RunID, "--plan-hash", d.PlanHash},
	} {
		e, code, _ := runBootstrapJSON(t, c, dir, args...)
		if code != ExitUsage || e.OK || !strings.Contains(e.Error.Message, "moved") {
			t.Fatalf("%v: a plan describes the commit it was made at: code %d %+v", args, code, e)
		}
	}
	assertNoWorkHandedOut(t, c, dir, d.RunID, "after the repository moved")
}

func TestConfirmRecomputesThePlanHashFromTheStoredRun(t *testing.T) {
	// The stored plan_hash column is not what the person's hash is compared with: the hash is
	// rebuilt from the stored sources and components, so a store that no longer describes the
	// plan that was shown is refused even when the hash the person passes equals the column.
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	d := decodeData[planData](t, pe)
	if n := execDB(t, c, `UPDATE bs_sources SET hash='tampered' WHERE run_id=? AND disposition='queued'`, d.RunID); n == 0 {
		t.Fatal("the fixture has no queued source to change")
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash)
	if code != ExitUsage || e.OK {
		t.Fatalf("a changed store must not confirm under the old hash: code %d %+v", code, e)
	}
	assertNoWorkHandedOut(t, c, dir, d.RunID, "after the stored sources changed")

	// A stored hash that was rewritten to match nothing is refused too.
	pe2, _, _ := runBootstrapJSON(t, c, dir, "plan")
	d2 := decodeData[planData](t, pe2)
	if n := execDB(t, c, `UPDATE bs_runs SET plan_hash=? WHERE run_id=?`, "feedface", d2.RunID); n != 1 {
		t.Fatalf("rewrote %d runs", n)
	}
	for _, h := range []string{"feedface", d2.PlanHash} {
		if e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d2.RunID, "--plan-hash", h); code != ExitUsage || e.OK {
			t.Fatalf("hash %q against a rewritten column: code %d %+v", h, code, e)
		}
	}
	assertNoWorkHandedOut(t, c, dir, d2.RunID, "after the stored hash was rewritten")
}

func TestARefusedFullConfirmLeavesASampledRunUnconfirmed(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[planData](t, pe)
	if _, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash); code != ExitUsage {
		t.Fatal("a sampled run cannot jump to the full run")
	}
	assertNoWorkHandedOut(t, c, dir, d.RunID, "after a refused full confirm")
}

func TestConfirmSampleTakesNoValue(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[planData](t, pe)
	for _, flag := range []string{"--sample=false", "--sample=0"} {
		if e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash, flag); code != ExitUsage || e.OK {
			t.Fatalf("%s: code %d %+v", flag, code, e)
		}
	}
	assertNoWorkHandedOut(t, c, dir, d.RunID, "after refused flag forms")
}
