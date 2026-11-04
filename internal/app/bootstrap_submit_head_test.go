package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A verdict's line range is checked against the blob the plan recorded, and the validator
// records the blob at HEAD. They agree only while HEAD is the planned commit, so a submission
// after the repository moved is refused and records nothing.
func TestSubmitRefusesARunWhoseRepositoryMoved(t *testing.T) {
	dir, c := bootstrapRepo(t)
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	run := decodeData[planData](t, pe).RunID
	forceState(t, c, run, "running")
	ne, _, _ := runBootstrapJSON(t, c, dir, "next", "--run", run, "--max", "50")
	nd := decodeData[leased](t, ne)
	if len(nd.Items) == 0 {
		t.Fatal("nothing leased")
	}
	if err := os.WriteFile(filepath.Join(dir, "later.go"), []byte("package later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "later.go")
	gitIn(t, dir, "commit", "-qm", "moved")
	var results []map[string]any
	for _, it := range nd.Items {
		results = append(results, map[string]any{"item": it.Item, "epoch": it.Epoch, "claims": []any{}})
	}
	body, _ := json.Marshal(map[string]any{"stage": "extract", "results": results})
	c.stdin = string(body)
	e, code, _ := runBootstrapJSON(t, c, dir, "submit", "--run", run, "--batch", nd.Batch)
	if code != ExitUsage || e.OK || !strings.Contains(e.Error.Message, "the repository moved") {
		t.Fatalf("a submission after the repository moved must be refused: %d %+v", code, e)
	}
	st, _, _ := runBootstrapJSON(t, c, dir, "status", "--run", run)
	if items := decodeData[struct {
		Items map[string]map[string]int `json:"items"`
	}](t, st).Items; items["extract"]["done"] != 0 || items["extract"]["leased"] != len(nd.Items) {
		t.Fatalf("nothing is recorded and the items stay leased: %+v", items)
	}
}
