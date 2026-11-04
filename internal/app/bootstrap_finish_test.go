package app

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// verifyPrepStderr is what the next call that prepared the verify items wrote to stderr.
var verifyPrepStderr string

// finishedRun drives plan, extraction (one doc claim), verification (confirmed) and returns the
// run id. Everything goes through the CLI.
func finishedRunInputs(t *testing.T) (string, *cli, string) {
	t.Helper()
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	run := decodeData[planData](t, pe).RunID
	forceState(t, c, run, "running")
	type item struct {
		Item  string `json:"item"`
		Ref   string `json:"ref"`
		Epoch int    `json:"epoch"`
	}
	type nextData struct {
		Batch string `json:"batch"`
		Items []item `json:"items"`
	}
	ne, _, _ := runBootstrapJSON(t, c, dir, "next", "--run", run, "--max", "50")
	nd := decodeData[nextData](t, ne)
	var results []map[string]any
	for _, it := range nd.Items {
		cl := []map[string]any{}
		if it.Ref == "src:repo:docs/design.md" {
			cl = append(cl, map[string]any{"text": "The internal/queue package acknowledges after the handler returns.", "kind": "current-impl",
				"excerpt": "acknowledges after the handler returns", "locator": "L1-L1"})
		}
		results = append(results, map[string]any{"item": it.Item, "epoch": it.Epoch, "claims": cl})
	}
	body, _ := json.Marshal(map[string]any{"stage": "extract", "results": results})
	c.stdin = string(body)
	if _, code, _ := runBootstrapJSON(t, c, dir, "submit", "--run", run, "--batch", nd.Batch); code != ExitOK {
		t.Fatal("extract submit failed")
	}
	ve, _, nextErr := runBootstrapJSON(t, c, dir, "next", "--run", run)
	verifyPrepStderr = nextErr
	vd := decodeData[nextData](t, ve)
	if len(vd.Items) != 1 {
		t.Fatalf("expected one verify item: %+v", vd)
	}
	return dir, c, run
}

func TestFinishPublishesPagesAndTheyAreSearchable(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	// Leave the verify item unsubmitted: finish must refuse while verification work remains.
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code == ExitOK || e.OK || !strings.Contains(e.Error.Message, "verify") {
		t.Fatalf("finish with open verify work must refuse and say so: %d %+v", code, e)
	}
	forceItemsClosed(t, c, run)
	e, code, _ = runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK || !e.OK {
		t.Fatalf("%d %+v", code, e)
	}
	rep := decodeData[struct {
		Pages []struct {
			Path    string `json:"path"`
			Outcome string `json:"outcome"`
		} `json:"pages"`
		EndMarker string `json:"end_marker"`
		Claims    map[string]int
	}](t, e)
	if rep.EndMarker == "" || len(rep.Pages) < 5 {
		t.Fatalf("%+v", rep)
	}
	overview := filepath.Join(dir, "project-memory", "wiki", "bootstrap", "overview.md")
	if got := readFile(t, overview); !strings.Contains(got, "origin: bootstrap") || !strings.Contains(got, "wikory bootstrap") {
		t.Fatalf("%s", got)
	}
	out, _, code := c.run(dir, "search", "acknowledges")
	if code != ExitOK || !strings.Contains(out, "acknowledges after the handler returns") || !strings.Contains(out, "unverified") {
		t.Fatalf("baseline claims are retrievable with their status:\n%s", out)
	}
	if st := gitIn(t, dir, "status", "--porcelain", "--", "project-memory/wiki/bootstrap"); !strings.Contains(st, "bootstrap") {
		t.Fatalf("pages are left as uncommitted files for a person to review: %q", st)
	}
	if again, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code == ExitOK && again.OK {
		t.Fatal("a finished run cannot be finished twice")
	}
}

func TestFinishNeverOverwritesAHumanEditedPage(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	if _, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code != ExitOK {
		t.Fatal("first finish")
	}
	constraints := filepath.Join(dir, "project-memory", "wiki", "bootstrap", "constraints.md")
	edited := readFile(t, constraints) + "\nA human added this line.\n"
	if err := os.WriteFile(constraints, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	// A second bootstrap of the same HEAD.
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	run2 := decodeData[planData](t, pe).RunID
	forceState(t, c, run2, "running")
	forceItemsClosed(t, c, run2)
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run2)
	if code != ExitOK {
		t.Fatalf("%d %+v", code, e)
	}
	rep := decodeData[struct {
		Pages []struct {
			Path    string `json:"path"`
			Outcome string `json:"outcome"`
		} `json:"pages"`
	}](t, e)
	conflicts := 0
	for _, p := range rep.Pages {
		if strings.HasSuffix(p.Path, "constraints.md") && p.Outcome == "conflict" {
			conflicts++
		}
	}
	if conflicts != 1 || readFile(t, constraints) != edited {
		t.Fatalf("the edited page must be reported as a conflict and left byte-for-byte as it was: %+v", rep.Pages)
	}
}

func TestFinishRefusesWhenTheRepositoryMovedSinceThePlan(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	if err := os.WriteFile(filepath.Join(dir, "later.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "later")
	e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitUsage || !strings.Contains(e.Error.Message, "moved") {
		t.Fatalf("the baseline describes the planned commit, so a moved HEAD means plan again: %d %+v", code, e)
	}
}

// steppingClock moves on by step every time it is read, so quick work looks slow to progress.
func steppingClock(step time.Duration) func() time.Time {
	now := time.Unix(0, 0)
	return func() time.Time { now = now.Add(step); return now }
}

// openTestDB opens the private store's database the way forceItemsClosed does.
func openTestDB(t *testing.T, c *cli) *sql.DB {
	t.Helper()
	var dbPath string
	_ = filepath.WalkDir(c.dataDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "wikory.db" {
			dbPath = p
		}
		return nil
	})
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// plantClaimRow stores a claim for a run directly, as if an earlier version or a bug had let it in.
func plantClaimRow(t *testing.T, c *cli, runID, text string) {
	t.Helper()
	db := openTestDB(t, c)
	defer db.Close()
	var checkout, item string
	if err := db.QueryRow(`SELECT checkout_id,item_id FROM bs_items WHERE run_id=? ORDER BY rowid LIMIT 1`, runID).Scan(&checkout, &item); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO bs_claims(run_id,checkout_id,item_id,ordinal,claim_id,text,kind,status,meta,tier) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		runID, checkout, item, 9999, "clm_planted", text, "current-impl", "unverified", "", "implementation"); err != nil {
		t.Fatal(err)
	}
}

func TestFinishRefusesToPublishAPageThatWouldHoldASecret(t *testing.T) {
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	secret := "hunter2hunter2"
	plantClaimRow(t, c, run, "The deploy script sets pass"+"word="+secret+" before the queue starts.")
	e, code, errOut := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	b, _ := json.Marshal(e)
	if code == ExitOK || e.OK {
		t.Fatalf("finish must refuse when a rendered page would hold a secret-shaped value: %d %s", code, b)
	}
	if strings.Contains(string(b)+errOut, secret) {
		t.Fatalf("neither stdout nor stderr may repeat the value:\n%s\n%s", b, errOut)
	}
	if !strings.Contains(e.Error.Message, "nothing was published") || !strings.Contains(e.Error.Message, "project-memory/wiki/bootstrap/") {
		t.Fatalf("the refusal names the page and says nothing was written: %q", e.Error.Message)
	}
	if _, err := os.Stat(filepath.Join(dir, "project-memory", "wiki", "bootstrap")); err == nil {
		t.Fatal("nothing may have been published")
	}
}

func TestNextAndFinishShowProgressForTheirLongSteps(t *testing.T) {
	old := progressNow
	progressNow = steppingClock(3 * time.Second)
	t.Cleanup(func() { progressNow = old })
	dir, c, run := finishedRunInputs(t)
	if !strings.Contains(verifyPrepStderr, `"stage_name":"prepare-verification"`) {
		t.Fatalf("next announces the verify preparation when it has real work to do: %q", verifyPrepStderr)
	}
	forceItemsClosed(t, c, run)
	_, code, errOut := runBootstrapJSON(t, c, dir, "finish", "--run", run)
	if code != ExitOK {
		t.Fatalf("finish: %d %s", code, errOut)
	}
	for _, want := range []string{`"stage_name":"prepare"`, `"stage_name":"publish"`, `"event":"progress"`} {
		if !strings.Contains(errOut, want) {
			t.Errorf("finish should report %s on stderr, got:\n%s", want, errOut)
		}
	}
}
