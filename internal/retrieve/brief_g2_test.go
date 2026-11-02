package retrieve

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func statusLine(out string) string { return strings.SplitN(out, "\n", 2)[0] }

// Claims that were not checked are unknown, not "not stale".
func TestBriefStaleCountSaysWhatItDidNotEvaluate(t *testing.T) {
	item := func(stale, evaluated, unknown bool) OrientItem {
		return OrientItem{Type: "decision", Title: "T", OneLine: "x", RelPath: "p.md", Stale: stale, Evaluated: evaluated, Unknown: unknown}
	}
	cases := map[string]struct {
		items []OrientItem
		want  string
	}{
		"all evaluated":         {[]OrientItem{item(true, true, false), item(false, true, false)}, "stale 1."},
		"some unknown":          {[]OrientItem{item(true, true, false), item(false, false, true), item(false, false, true)}, "stale 1, 2 unknown."},
		"none evaluated":        {[]OrientItem{item(false, false, true), item(false, false, true)}, "stale unknown."},
		"items without a claim": {[]OrientItem{item(false, false, false)}, "stale 0."},
	}
	for name, c := range cases {
		in := many(0, 0)
		in.Orientation = c.items
		if line := statusLine(Brief(in)); !strings.Contains(line, c.want) {
			t.Errorf("%s: want %q in %q", name, c.want, line)
		}
	}
}

func TestBriefStatusLineShowsHookErrorsOnlyWhenThereAreSome(t *testing.T) {
	in := many(1, 1)
	if line := statusLine(Brief(in)); strings.Contains(line, "hook errors") {
		t.Fatalf("%q", line)
	}
	in.HookErrors = 3
	if line := statusLine(Brief(in)); !strings.Contains(line, "hook errors 3") {
		t.Fatalf("hook errors missing from %q", line)
	}
}

// Pending holds only the newest rows; PendingTotal is the truth, and "more available" uses it.
func TestBriefPendingUsesTheTrueTotal(t *testing.T) {
	in := many(50, 0)
	in.PendingTotal = 80
	out := Brief(in)
	rendered := strings.Count(out, "[captured] request number")
	m := regexp.MustCompile(`(\d+) more available`).FindStringSubmatch(out)
	if !strings.Contains(statusLine(out), "pending 80 ") || m == nil || m[1] != fmt.Sprint(80-rendered) {
		t.Fatalf("rendered=%d more=%v: %s", rendered, m, out)
	}
}
