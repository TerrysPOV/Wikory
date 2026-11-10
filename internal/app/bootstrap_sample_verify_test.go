package app

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

type sampleMeasure struct {
	Items        int `json:"sample_items"`
	Done         int `json:"sample_done"`
	VerifyItems  int `json:"verify_items"`
	VerifyDone   int `json:"verify_done"`
	VerifyTokens int `json:"verify_tokens"`
	Verdicts     struct {
		Undetermined int `json:"not_determinable"`
	} `json:"verdicts"`
}

// Terry's ruling (2026-11-10): the sample runs the paid pipeline on its items, extract and
// verify, and publishes nothing. Through the CLI: the sample's claim name a component, so the
// sample hands out a verify item before it settles, and finish on the settled sample reports the
// measurements, writes no page and leaves the run awaiting the full run's confirmation.
func TestASampleVerifiesItsClaimsAndFinishReportsItWithoutPublishing(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[planData](t, pe)
	if e, code, _ := runBootstrapJSON(t, c, dir, "confirm", "--run", d.RunID, "--plan-hash", d.PlanHash, "--sample"); code != ExitOK || !e.OK {
		t.Fatalf("confirm --sample: %d %+v", code, e)
	}
	if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", d.RunID); code == ExitOK || e.Error == nil || !strings.Contains(e.Error.Message, "the sample is still running") {
		t.Fatalf("finish on a sample that is still running must refuse and say so: %d %+v", code, e)
	}
	ne, _, _ := runBootstrapJSON(t, c, dir, "next", "--run", d.RunID, "--max", "8")
	first := decodeData[leased](t, ne)
	if len(first.Items) != 1 {
		t.Fatalf("the sample hands out its one extract item: %+v", first)
	}
	claims := map[string]string{
		"src:repo:docs/design.md":                          `{"text":"The internal/queue package acknowledges after the handler returns.","kind":"current-impl","excerpt":"acknowledges after the handler returns","locator":"L1-L1"}`,
		"src:repo:.claude/handoffs/PRIOR_SESSION_STATE.md": `{"text":"Retry in internal/queue is an open item.","kind":"hypothesis","excerpt":"Open item: internal/queue retry.","locator":"L1-L1"}`,
	}
	it := first.Items[0]
	if claims[it.Ref] == "" {
		t.Fatalf("the fixture's sampled item is one of its two documents: %+v", it)
	}
	c.stdin = fmt.Sprintf(`{"stage":"extract","results":[{"item":%q,"epoch":%d,"claims":[%s],"usage":{"input_tokens":1000,"output_tokens":100}}]}`, it.Item, it.Epoch, claims[it.Ref])
	if e, code, _ := runBootstrapJSON(t, c, dir, "submit", "--run", d.RunID, "--batch", first.Batch); code != ExitOK || !e.OK {
		t.Fatalf("submit extract: %d %+v", code, e)
	}
	ve, code, _ := runBootstrapJSON(t, c, dir, "next", "--run", d.RunID, "--max", "8")
	vb := decodeData[leased](t, ve)
	if code != ExitOK || len(vb.Items) != 1 || vb.Items[0].Ref != "sample:comp:internal/queue" {
		t.Fatalf("the sample hands out a verify item for the component its claim names: %d %+v", code, vb)
	}
	c.stdin = fmt.Sprintf(`{"stage":"verify","results":[{"item":%q,"epoch":%d,"verdicts":[],"usage":{"input_tokens":3000,"output_tokens":300}}]}`, vb.Items[0].Item, vb.Items[0].Epoch)
	if e, code, _ := runBootstrapJSON(t, c, dir, "submit", "--run", d.RunID, "--batch", vb.Batch); code != ExitOK || !e.OK {
		t.Fatalf("submit verify: %d %+v", code, e)
	}

	fe, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", d.RunID)
	rep := decodeData[struct {
		State     string        `json:"state"`
		Published int           `json:"pages_published"`
		Sample    sampleMeasure `json:"sample"`
	}](t, fe)
	if code != ExitOK || !fe.OK || rep.State != "awaiting-confirm" || rep.Published != 0 {
		t.Fatalf("finish on a settled sample reports it and leaves the run awaiting confirmation: %d %+v", code, rep)
	}
	if m := rep.Sample; m.Items != 1 || m.Done != 1 || m.VerifyItems != 1 || m.VerifyDone != 1 || m.VerifyTokens != 3300 || m.Verdicts.Undetermined != 1 {
		t.Fatalf("the sample's report covers its verification: %+v", m)
	}
	out, _, code := c.run(dir, "bootstrap", "finish", "--run", d.RunID)
	if code != ExitOK || !strings.Contains(out, "nothing was published") || !strings.Contains(out, "1 not determinable") || !strings.Contains(out, "verify 1/1 items") {
		t.Fatalf("the text says what the sample measured and that nothing was published: %s", out)
	}
	_ = filepath.WalkDir(filepath.Join(dir, "project-memory"), func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && strings.Contains(filepath.Base(p), "bootstrap") {
			t.Errorf("a sample must publish no page, found %s", p)
		}
		return nil
	})
}

// The plan shows the sample's cost apart from the full run's: its extract items and their
// verification, in the JSON and the text.
func TestPlanShowsTheSampleCostApartFromTheFullRun(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan", "--sample", "1")
	d := decodeData[struct {
		Sample []struct {
			Stage string `json:"stage"`
			Items int    `json:"items"`
		} `json:"sample_estimates"`
	}](t, pe)
	if len(d.Sample) != 2 || d.Sample[0].Stage != "extract" || d.Sample[0].Items != 1 || d.Sample[1].Stage != "verify" || d.Sample[1].Items != 1 {
		t.Fatalf("the sample's figure covers one extract item and its verification: %+v", d.Sample)
	}
	out, _, _ := c.run(dir, "bootstrap", "plan", "--sample", "1")
	if !strings.Contains(out, "Sample of 1 extract item(s), run through extraction and the verification") || !strings.Contains(out, "it publishes nothing") ||
		!strings.Contains(out, "To sample (extract and verify, nothing published)") {
		t.Fatalf("the plan text shows the sample's cost and scope: %s", out)
	}
	full, _, _ := runBootstrapJSON(t, c, dir, "plan")
	if strings.Contains(fmt.Sprint(full.Data), "sample_estimates") {
		t.Fatal("a plan without a sample has no sample figure")
	}
}
