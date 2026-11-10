package app

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
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
	e, code, _ = runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash)
	if code != ExitUsage || e.OK || !strings.Contains(e.Error.Message, "the sample is still running") {
		t.Fatalf("the full run cannot start while the sample is still running: %d %+v", code, e)
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
	// Fix wave 2 (2026-11-10): the sample runs extraction and verification and publishes nothing.
	if !strings.Contains(out, "extraction and verification, nothing published") || !strings.Contains(out, "projected verification for the full run") {
		t.Fatalf("the status must say the sample covers extraction and verification: %s", out)
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
	if code != ExitUsage || e.OK || !strings.Contains(e.Error.Message, "stored run no longer matches") {
		t.Fatalf("a changed store must not confirm under the old hash, and the remedy is to plan again, not to read the hash again: code %d %+v", code, e)
	}
	assertNoWorkHandedOut(t, c, dir, d.RunID, "after the stored sources changed")

	// A stored hash that was rewritten to match nothing is refused too.
	pe2, _, _ := runBootstrapJSON(t, c, dir, "plan")
	d2 := decodeData[planData](t, pe2)
	if n := execDB(t, c, `UPDATE bs_runs SET plan_hash=? WHERE run_id=?`, "feedface", d2.RunID); n != 1 {
		t.Fatalf("rewrote %d runs", n)
	}
	for _, h := range []string{"feedface", d2.PlanHash} {
		// The hash the person was shown, and the rewritten column's own value, both get the
		// plan-again remedy: status would print the same hash, so a driver told to read it
		// again would loop.
		if e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d2.RunID, "--plan-hash", h); code != ExitUsage || e.OK || !strings.Contains(e.Error.Message, "stored run no longer matches") {
			t.Fatalf("hash %q against a rewritten column must say the stored run no longer matches: code %d %+v", h, code, e)
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

func TestAMistypedHashOnAnUntouchedStorePointsAtWhereTheHashIsPrinted(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	d := decodeData[planData](t, pe)
	e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", "deadbeef")
	if code != ExitUsage || e.OK {
		t.Fatalf("code %d %+v", code, e)
	}
	msg := e.Error.Message
	if !strings.Contains(msg, "is not this plan's hash") || strings.Contains(msg, "plan again") {
		t.Fatalf("a mistyped hash on a sound store is the person's slip, not a damaged run: %q", msg)
	}
	// The message names what prints the hash, and that really does.
	if !strings.Contains(msg, "bootstrap status --json") || !strings.Contains(msg, "run.plan_hash") {
		t.Fatalf("the message must say where the hash is printed: %q", msg)
	}
	st, _, _ := runBootstrapJSON(t, c, dir, "status", "--run", d.RunID)
	got := decodeData[struct {
		Run struct {
			PlanHash string `json:"plan_hash"`
		} `json:"run"`
	}](t, st).Run.PlanHash
	if got != d.PlanHash || got == "" {
		t.Fatalf("status --json run.plan_hash = %q, want %q", got, d.PlanHash)
	}
	assertNoWorkHandedOut(t, c, dir, d.RunID, "after a mistyped hash")
}

// sourceTree writes files (relative path -> content) under a fresh directory outside the repository.
func sourceTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The rebuilt plan hash reads sources back from the store in SQL order. Names and paths whose
// byte order differs from a case-folding or punctuation-skipping order prove that order is the
// one inventory produced; if it were not, every run planned with --source would be refused.
func TestConfirmAcceptsARunPlannedWithSourcesWhoseNamesSortAwkwardly(t *testing.T) {
	dir, c := bootstrapRepo(t)
	notes := sourceTree(t, map[string]string{
		"a.md": "Alpha note.\n", "B.md": "Bravo note.\n", "Z.md": "Zulu note.\n",
		"_x.md": "Underscore first.\n", "-y.md": "Dash first.\n",
		"a_b.md": "Underscore inside.\n", "a-b.md": "Dash inside.\n",
		"\u00e9.md": "Accented.\n", "sub/Q.md": "Upper in a folder.\n", "sub/q.md": "Lower in a folder.\n",
	})
	more := sourceTree(t, map[string]string{"one.md": "One.\n", "Two.md": "Two.\n"})
	pe, _, errOut := runBootstrapJSON(t, c, dir, "plan", "--source", "notes="+notes, "--source", "notes-2="+more)
	d := decodeData[planData](t, pe)
	if d.RunID == "" || d.Scope != "repo+notes+notes-2" {
		t.Fatalf("%+v %s", d, errOut)
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash)
	if code != ExitOK || !e.OK {
		t.Fatalf("a run planned with --source must confirm under the hash it was shown: %d %+v", code, e)
	}
	if _, code, _ := runBootstrapJSON(t, c, dir, "next", "--run", d.RunID); code != ExitOK {
		t.Fatal("a confirmed run hands out work")
	}
}

type leased struct {
	Batch string `json:"batch"`
	Items []struct {
		Item  string `json:"item"`
		Ref   string `json:"ref"`
		Epoch int    `json:"epoch"`
	} `json:"items"`
}

// submitLeased submits one result per leased item. The two documents of the fixture carry a real
// claim; any other item is submitted with none, which is a valid result.
func submitLeased(t *testing.T, c *cli, dir, runID string, b leased, in, out int) {
	t.Helper()
	claims := map[string]string{
		"src:repo:docs/design.md":                          `{"text":"The queue acknowledges after the handler returns.","kind":"current-impl","excerpt":"acknowledges after the handler returns","locator":"L1-L1"}`,
		"src:repo:.claude/handoffs/PRIOR_SESSION_STATE.md": `{"text":"Queue retry is an open item.","kind":"hypothesis","excerpt":"Open item: internal/queue retry.","locator":"L1-L1"}`,
	}
	var rs []string
	for i, it := range b.Items {
		usage := `{"input_tokens":0,"output_tokens":0}`
		if i == 0 {
			usage = fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d}`, in, out)
		}
		rs = append(rs, fmt.Sprintf(`{"item":%q,"epoch":%d,"claims":[%s],"usage":%s}`, it.Item, it.Epoch, claims[it.Ref], usage))
	}
	c.stdin = `{"stage":"extract","results":[` + strings.Join(rs, ",") + `]}`
	if e, code, _ := runBootstrapJSON(t, c, dir, "submit", "--run", runID, "--batch", b.Batch); code != ExitOK || !e.OK {
		t.Fatalf("submit: %d %+v", code, e)
	}
}

type nextData struct {
	Items  []struct{ Item string } `json:"items"`
	Status string                  `json:"status"`
}

type sampleStatus struct {
	Run struct {
		State    string `json:"state"`
		TokensIn int    `json:"tokens_in"`
	} `json:"run"`
	Sample struct {
		Items int     `json:"sample_items"`
		Done  int     `json:"sample_done"`
		In    int     `json:"tokens_in"`
		Out   int     `json:"tokens_out"`
		Rate  float64 `json:"acceptance_rate"`
	} `json:"sample"`
}

// The whole sequence a person walks through: sample, measure, then the full run. The CLI is the
// only code that picks the source state of the full-run confirm, so this is the test that fails
// if a sampled run in awaiting-confirm can never be confirmed.
func TestASampledRunReachesTheFullRunAndTheSampleFiguresStayFrozen(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[planData](t, pe)
	extractItems := 0
	for _, e := range d.Estimates {
		if e.Stage == "extract" {
			extractItems = e.Items
		}
	}
	if extractItems < 2 {
		t.Fatalf("the fixture needs at least two extract items: %+v", d.Estimates)
	}
	if e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash, "--sample"); code != ExitOK || !e.OK {
		t.Fatalf("confirm --sample: %d %+v", code, e)
	}
	ne, _, _ := runBootstrapJSON(t, c, dir, "next", "--run", d.RunID, "--max", "50")
	first := decodeData[leased](t, ne)
	if len(first.Items) != 1 {
		t.Fatalf("the sample is the one item asked for: %+v", first)
	}
	submitLeased(t, c, dir, d.RunID, first, 1000, 100)
	// Fix wave 2 (2026-11-10): the sample's claim names no component, so the next call finds
	// nothing to verify and settles the sample, answering with an empty batch and the new state.
	se, code, _ := runBootstrapJSON(t, c, dir, "next", "--run", d.RunID, "--max", "50")
	if settledNext := decodeData[nextData](t, se); code != ExitOK || len(settledNext.Items) != 0 || !strings.Contains(settledNext.Status, " is awaiting-confirm:") {
		t.Fatalf("the next call that settles a sample answers with no items and the settled state: %d %+v", code, settledNext)
	}

	status := func() sampleStatus {
		t.Helper()
		st, _, _ := runBootstrapJSON(t, c, dir, "status", "--run", d.RunID)
		return decodeData[sampleStatus](t, st)
	}
	settled := status()
	if settled.Run.State != "awaiting-confirm" || settled.Sample.Done != 1 || settled.Sample.In != 1000 || settled.Sample.Out != 100 {
		t.Fatalf("the sample should have settled with its own figures: %+v", settled)
	}

	// The person says yes to the full run, without --sample.
	if e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash); code != ExitOK || !e.OK {
		t.Fatalf("a sampled run in awaiting-confirm must confirm to the full run: %d %+v", code, e)
	}
	re, _, _ := runBootstrapJSON(t, c, dir, "next", "--run", d.RunID, "--max", "50")
	rest := decodeData[leased](t, re)
	if len(rest.Items) != extractItems-1 {
		t.Fatalf("the full run leases the %d items the sample did not: got %d", extractItems-1, len(rest.Items))
	}
	submitLeased(t, c, dir, d.RunID, rest, 7000, 700)

	after := status()
	if after.Run.TokensIn != 8000 {
		t.Fatalf("the run total should include the full run's tokens: %+v", after)
	}
	if after.Sample != settled.Sample {
		t.Fatalf("the sample figures must stay what the sample measured: settled %+v, after the full run %+v", settled.Sample, after.Sample)
	}
}
