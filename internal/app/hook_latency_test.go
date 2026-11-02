package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func slack() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("WIKORY_LATENCY_SLACK")); err == nil && n > 0 {
		return time.Duration(n)
	}
	return 1
}

// A fixture wiki of 300 pages with 8 claims each, larger than the pilot is expected to be in 1a.
func writeFixtureWiki(t *testing.T, dir string) {
	t.Helper()
	for i := 0; i < 300; i++ {
		var b strings.Builder
		fmt.Fprintf(&b, "---\ntype: %s\ntitle: Page %03d\n---\n# Page %03d\n", []string{"decision", "component", "concept"}[i%3], i, i)
		for j := 0; j < 8; j++ {
			fmt.Fprintf(&b, "## Section %d\n<!-- claim kind=intent status=unverified -->\nClaim %d of page %d mentions enqueueJob and retry policy number %d.\n\n", j, j, i, i*8+j)
		}
		p := filepath.Join(dir, "project-memory", "wiki", fmt.Sprintf("p%03d.md", i))
		if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionStartWithinTwoSecondsOnAFixtureWiki(t *testing.T) {
	if raceEnabled {
		t.Skip("latency budgets are measured without the race detector, which slows code several-fold")
	}
	h := newHookRig(t)
	writeFixtureWiki(t, h.dir)
	budget := 2 * time.Second * slack()
	for _, phase := range []string{"cold index", "warm index"} {
		start := time.Now()
		got := h.context(t, h.dir)
		el := time.Since(start)
		t.Logf("SessionStart %s: %v", phase, el)
		if el > budget || strings.Contains(got, "degraded") || !strings.Contains(got, "Page 000") {
			t.Fatalf("%s: elapsed %v (budget %v) degraded=%v", phase, el, budget, strings.Contains(got, "degraded"))
		}
	}
}

func TestStopCaptureWithinBudget(t *testing.T) {
	if raceEnabled {
		t.Skip("latency budgets are measured without the race detector, which slows code several-fold")
	}
	h := newHookRig(t)
	h.hook(t, h.dir, "user-prompt-submit", h.payload("UserPromptSubmit", h.dir, map[string]any{"prompt": "x", "prompt_id": "p1"}))
	start := time.Now()
	h.hook(t, h.dir, "stop", h.payload("Stop", h.dir, map[string]any{"prompt_id": "p1", "last_assistant_message": "done"}))
	el := time.Since(start)
	t.Logf("Stop capture: %v (target 300ms, bound 1s)", el)
	if el > time.Second*slack() {
		t.Fatalf("Stop capture took %v", el)
	}
}
