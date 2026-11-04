package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/publish"
)

// pagesRecorded counts the rows of bs_pages: what earlier bootstraps recorded as their own.
func pagesRecorded(t *testing.T, c *cli) int {
	t.Helper()
	db := openTestDB(t, c)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM bs_pages`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type finishReport struct {
	Pages []struct {
		Path    string `json:"path"`
		Outcome string `json:"outcome"`
	} `json:"pages"`
	StalePages []string       `json:"stale_pages"`
	Unindexed  []string       `json:"unindexed_claims"`
	EndMarker  string         `json:"end_marker"`
	Claims     map[string]int `json:"claims"`
	Proposals  struct {
		Basis string `json:"basis"`
	} `json:"proposals"`
	TokensNote string `json:"tokens_note"`
}

// plantClaimOn stores a claim as if the item with this ref had produced it.
func plantClaimOn(t *testing.T, c *cli, runID, ref, text string) {
	t.Helper()
	db := openTestDB(t, c)
	defer db.Close()
	var checkout, item string
	if err := db.QueryRow(`SELECT checkout_id,item_id FROM bs_items WHERE run_id=? AND stage='extract' AND ref=?`, runID, ref).Scan(&checkout, &item); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bs_claims(run_id,checkout_id,item_id,ordinal,claim_id,text,kind,status,meta,tier) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		runID, checkout, item, 9998, "clm_planted_comp", text, "current-impl", "unverified", "", "implementation"); err != nil {
		t.Fatal(err)
	}
}

func TestFinishRefusalNamesAComponentPageOnlyByItsHashAndWritesNothing(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	secret := "hunter2hunter2"
	// internal/queue is an inspected component, so a claim naming it lands on its own page.
	plantClaimOn(t, c, run, "comp:internal/queue", "The internal/queue deploy script sets pass"+"word="+secret+" first.")
	e, code, errOut := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	b, _ := json.Marshal(e)
	if code == ExitOK || e.OK {
		t.Fatalf("finish must refuse: %d %s", code, b)
	}
	all := string(b) + errOut
	if strings.Contains(all, secret) {
		t.Fatalf("the value is never repeated:\n%s", all)
	}
	hash := ids.SHA256Hex([]byte("internal/queue"))[:6]
	if !strings.Contains(e.Error.Message, hash) || !strings.Contains(e.Error.Message, "nothing was published") {
		t.Fatalf("a component page is named by its hash suffix: %q", e.Error.Message)
	}
	for _, leak := range []string{"queue", "component-", "internal"} {
		if strings.Contains(all, leak) {
			t.Fatalf("the refusal must not repeat what the page was named after (%q):\n%s", leak, all)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "project-memory", "wiki", "bootstrap")); err == nil {
		t.Fatal("nothing may have been written to the repository")
	}
	if n := pagesRecorded(t, c); n != 0 {
		t.Fatalf("nothing may be recorded as published: %d rows", n)
	}
	run2, _, _ := runBootstrapJSON(t, c, dir, "status", "--run", run)
	if st := decodeData[struct {
		Run struct {
			State string `json:"state"`
		} `json:"run"`
	}](t, run2); st.Run.State == "done" {
		t.Fatal("a refused finish must not mark the run done")
	}
}

func TestFinishWithASecretOnAFixedPageRecordsAndWritesNothing(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	plantClaimRow(t, c, run, "The deploy script sets pass"+"word=hunter2hunter2 before the queue starts.")
	if _, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code == ExitOK {
		t.Fatal("finish must refuse")
	}
	if n := pagesRecorded(t, c); n != 0 {
		t.Fatalf("nothing may be recorded: %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "project-memory", "wiki", "bootstrap")); err == nil {
		t.Fatal("nothing may be written")
	}
}

func TestFinishReportsAClaimThePageHoldsButTheIndexCannotRead(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	plantClaimRow(t, c, run, strings.Repeat("`a`", 1100))
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK || !e.OK {
		t.Fatalf("%d %+v", code, e)
	}
	rep := decodeData[finishReport](t, e)
	if len(rep.Unindexed) != 1 || rep.Unindexed[0] != "clm_planted" {
		t.Fatalf("the claim the index cannot read is named, not silently published: %+v", rep.Unindexed)
	}
	noticed := false
	for _, n := range e.Notices {
		noticed = noticed || (strings.Contains(n, "clm_planted") && strings.Contains(n, "cannot read"))
	}
	if !noticed {
		t.Fatalf("finish says so as a notice: %v", e.Notices)
	}
}

// pagesOf reads every recorded page's hash and the bytes now on disk.
func recordedPages(t *testing.T, c *cli) map[string]string {
	t.Helper()
	db := openTestDB(t, c)
	defer db.Close()
	rows, err := db.Query(`SELECT path,hash FROM bs_pages`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, h string
		if err := rows.Scan(&p, &h); err != nil {
			t.Fatal(err)
		}
		out[p] = h
	}
	return out
}

func TestFinishRecordsTheHashOfTheBytesOnDiskAndRerunsAsUnchanged(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK {
		t.Fatalf("%d %+v", code, e)
	}
	rep := decodeData[finishReport](t, e)
	rec := recordedPages(t, c)
	if len(rec) != len(rep.Pages) || len(rec) == 0 {
		t.Fatalf("every page is recorded: %d recorded, %d reported", len(rec), len(rep.Pages))
	}
	for p, h := range rec {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		if publish.HashContent(data) != h {
			t.Fatalf("%s: the recorded hash is not the hash of the bytes on disk, so a later bootstrap would call its own page an edit", p)
		}
		if bytes.Contains(data, []byte("\r")) {
			t.Fatalf("%s: pages are written with the line endings they were rendered with", p)
		}
	}
	// A person's edit that keeps the very same bytes is not an edit: a rerun of the same render
	// (with nothing extracted, so a different render) never reports our own page as a conflict.
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	run2 := decodeData[planData](t, pe).RunID
	forceState(t, c, run2, "running")
	forceItemsClosed(t, c, run2)
	e2, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run2)
	if code != ExitOK {
		t.Fatalf("%d %+v", code, e2)
	}
	for _, p := range decodeData[finishReport](t, e2).Pages {
		if p.Outcome == "conflict" {
			t.Fatalf("%s: our own earlier output is not a conflict", p.Path)
		}
	}
}

func TestFinishListsPagesAnEarlierBootstrapWroteThatItDidNotRenderThisTime(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	db := openTestDB(t, c)
	var checkout string
	if err := db.QueryRow(`SELECT checkout_id FROM bs_runs WHERE run_id=?`, run).Scan(&checkout); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bs_pages(checkout_id,path,hash,run_id) VALUES(?,?,?,?)`, checkout, "project-memory/wiki/bootstrap/component-internal-gone-abcdef.md", "h", run); err != nil {
		t.Fatal(err)
	}
	db.Close()
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK {
		t.Fatalf("%d %+v", code, e)
	}
	rep := decodeData[finishReport](t, e)
	if len(rep.StalePages) != 1 || !strings.HasSuffix(rep.StalePages[0], "component-internal-gone-abcdef.md") {
		t.Fatalf("a page that shrank away or a component that dropped out is named: %+v", rep.StalePages)
	}
}

// lateSubmitBody is an extract submission for the doc item, as finishedRunInputs sent it.
func lateSubmitBody(t *testing.T, c *cli, runID string) (body, batch string) {
	t.Helper()
	db := openTestDB(t, c)
	defer db.Close()
	var item, bat string
	var epoch int
	if err := db.QueryRow(`SELECT item_id,epoch,batch FROM bs_items WHERE run_id=? AND stage='extract' AND ref='src:repo:docs/design.md'`, runID).Scan(&item, &epoch, &bat); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"stage": "extract", "results": []map[string]any{{"item": item, "epoch": epoch,
		"claims": []map[string]any{{"text": "The internal/queue package acknowledges after the handler returns.", "kind": "current-impl",
			"excerpt": "acknowledges after the handler returns", "locator": "L1-L1"}}}}})
	return string(b), bat
}

func claimCount(t *testing.T, c *cli, runID string) int {
	t.Helper()
	db := openTestDB(t, c)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM bs_claims WHERE run_id=?`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestASubmissionAfterFinishIsRefusedAndStoresNothing(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	if _, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code != ExitOK {
		t.Fatal("finish")
	}
	before := claimCount(t, c, run)
	body, batch := lateSubmitBody(t, c, run)
	// 1. The item is done: the old lease is refused.
	c.stdin = body
	e, _, _ := runBootstrapJSON(t, c, dir, "submit", "--run", run, "--batch", batch)
	if got := decodeData[submitReport](t, e); len(got.Results) != 1 || got.Results[0].Error == "" {
		t.Fatalf("a late submission is refused: %+v", got)
	}
	// 2. Even an item that is still leased, as if finish had run while it was out: the run is
	// not working any more, so nothing it sends is stored.
	db := openTestDB(t, c)
	if _, err := db.Exec(`UPDATE bs_items SET state='leased' WHERE run_id=? AND stage='extract' AND ref='src:repo:docs/design.md'`, run); err != nil {
		t.Fatal(err)
	}
	db.Close()
	c.stdin = body
	e, _, _ = runBootstrapJSON(t, c, dir, "submit", "--run", run, "--batch", batch)
	got := decodeData[submitReport](t, e)
	if len(got.Results) != 1 || got.Results[0].Error == "" || !strings.Contains(got.Results[0].Error, "done") {
		t.Fatalf("a leased item of a finished run is refused on the run's state: %+v", got)
	}
	if after := claimCount(t, c, run); after != before {
		t.Fatalf("a late submission stored claims: %d -> %d", before, after)
	}
}

func TestFinishPublishesInSeveralJobsWhenThePagesDoNotFitOne(t *testing.T) {
	old, oldKill := maxOpsPerJob, killPoint
	maxOpsPerJob = 2
	jobs := 0
	killPoint = func(step string) {
		if strings.HasPrefix(step, "after-job-") {
			jobs++
		}
	}
	t.Cleanup(func() { maxOpsPerJob, killPoint = old, oldKill })
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK {
		t.Fatalf("%d %+v", code, e)
	}
	rep := decodeData[finishReport](t, e)
	for _, p := range rep.Pages {
		if p.Outcome != "written" {
			t.Fatalf("%s: %s", p.Path, p.Outcome)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p.Path))); err != nil {
			t.Fatalf("%s was not written: %v", p.Path, err)
		}
	}
	if want := (len(rep.Pages) + 1) / 2; jobs != want {
		t.Fatalf("%d pages at 2 per job make %d jobs, got %d", len(rep.Pages), want, jobs)
	}
	if got := recordedPages(t, c); len(got) != len(rep.Pages) {
		t.Fatalf("every page of every job is recorded: %d of %d", len(got), len(rep.Pages))
	}
}

func TestStatusAndFinishSayThatAcceptedAndRejectedCountExtractSubmissionsOnly(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	out, _, code := c.run(dir, "bootstrap", "status", "--run", run)
	if code != ExitOK || !strings.Contains(out, "extract admissions") {
		t.Fatalf("status text:\n%s", out)
	}
	e, _, _ := runBootstrapJSON(t, c, dir, "status", "--run", run)
	if st := decodeData[struct {
		Note string `json:"admissions_note"`
	}](t, e); !strings.Contains(st.Note, "extract submissions only") {
		t.Fatalf("status JSON carries the note: %q", st.Note)
	}
	forceItemsClosed(t, c, run)
	e, code, _ = runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK {
		t.Fatalf("%d %+v", code, e)
	}
	rep := decodeData[finishReport](t, e)
	if !strings.Contains(rep.Proposals.Basis, "extract") || !strings.Contains(rep.TokensNote, "asserted") {
		t.Fatalf("finish labels the counts: %+v", rep)
	}
}

func TestProgressEventsOnStderrAreOneJSONObjectPerLineAndStdoutStaysOneEnvelope(t *testing.T) {
	old := progressNow
	progressNow = steppingClock(3 * 1e9)
	t.Cleanup(func() { progressNow = old })
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	out, errOut, code := c.run(dir, "--json", "bootstrap", "finish", "--run", run)
	if code != ExitOK {
		t.Fatalf("%d %s", code, errOut)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout must stay one envelope: %v\n%s", err, out)
	}
	events := 0
	for _, line := range strings.Split(strings.TrimSpace(errOut), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("every stderr line is a JSON event: %q", line)
		}
		if ev["event"] != nil {
			events++
		}
	}
	if events == 0 {
		t.Fatalf("expected progress events:\n%s", errOut)
	}
}
