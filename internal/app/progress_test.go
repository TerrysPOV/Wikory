package app

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

func TestProgressNoticeUpFrontWhenSlowExpected(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, false, false, clk.now)
	p.Begin("indexing", 2000, 4*time.Second, "First run: indexing 2000 pages. Estimated 4s. Safe to interrupt.")
	if !strings.Contains(b.String(), "First run: indexing 2000 pages") {
		t.Fatalf("no up-front notice: %q", b.String())
	}
}

func TestProgressNoticeOnlyWhenAQuickOperationRunsLong(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, false, false, clk.now)
	p.Begin("indexing", 10, 100*time.Millisecond, "Indexing 10 pages.")
	clk.t = clk.t.Add(time.Second)
	p.Step(5)
	if b.Len() != 0 {
		t.Fatalf("printed before two seconds: %q", b.String())
	}
	clk.t = clk.t.Add(2 * time.Second) // 3 s in: past the threshold
	p.Step(6)
	if !strings.Contains(b.String(), "Indexing 10 pages.") || !strings.Contains(b.String(), "6/10") {
		t.Fatalf("no late notice and progress: %q", b.String())
	}
}

func TestProgressJSONEvents(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, true, false, clk.now)
	p.Begin("indexing", 4, 5*time.Second, "Indexing 4 pages.")
	p.Step(2)
	p.End()
	if !strings.Contains(b.String(), `"event":"notice"`) || !strings.Contains(b.String(), `"event":"progress"`) || !strings.Contains(b.String(), `"done":2`) {
		t.Fatalf("%s", b.String())
	}
}
