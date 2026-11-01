package retrieve

import (
	"strings"
	"testing"
)

func TestExpand(t *testing.T) {
	cases := map[string][]string{
		"enqueueJob":   {"enqueuejob", "enqueue", "job"},
		"enqueue_job":  {"enqueuejob", "enqueue", "job"},
		"src/queue.ts": {"src", "queue", "ts", "srcqueuets"},
		"HTTPServer":   {"httpserver", "http", "server"},
		"v2Parser":     {"v2parser", "v2", "parser"},
	}
	for in, wants := range cases {
		got := " " + Expand(in) + " "
		for _, w := range wants {
			if !strings.Contains(got, " "+w+" ") {
				t.Errorf("Expand(%q)=%q lacks %q", in, got, w)
			}
		}
	}
}

func TestQueryWordsAreBoundedAndClean(t *testing.T) {
	w := QueryWords(`enqueue_job "drop table" ` + strings.Repeat("x ", 100))
	if len(w) > 32 {
		t.Fatalf("%d words", len(w))
	}
	for _, x := range w {
		if strings.ContainsAny(x, `"*:()^`) {
			t.Fatalf("word %q carries FTS syntax", x)
		}
	}
}
