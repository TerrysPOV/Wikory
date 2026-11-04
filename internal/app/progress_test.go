package app

import (
	"bytes"
	"os"
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

func TestIsTTYIsFalseForBuffersAndTheNullDevice(t *testing.T) {
	if IsTTY(&bytes.Buffer{}) {
		t.Fatal("a buffer is not a terminal")
	}
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip("no null device")
	}
	defer f.Close()
	if IsTTY(f) {
		t.Fatal("the null device is a character device but not a terminal")
	}
}

func TestProgressShowsStageNameAndAnEstimatedTimeLeft(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, false, false, clk.now)
	p.BeginStage(2, 5, "inventory", 100, 10*time.Second, "Reading 100 files.")
	clk.t = clk.t.Add(10 * time.Second)
	p.Step(25)
	out := b.String()
	if !strings.Contains(out, "stage 2 of 5: inventory") || !strings.Contains(out, "25/100") || !strings.Contains(out, "30s left (estimate)") {
		t.Fatalf("%q", out)
	}
}

func TestProgressJSONCarriesStageAndLabelsTheEstimate(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, true, false, clk.now)
	p.BeginStage(1, 3, "scan", 10, 10*time.Second, "Scanning.")
	clk.t = clk.t.Add(4 * time.Second)
	p.Step(2)
	out := b.String()
	for _, want := range []string{`"stage_name":"scan"`, `"stage_index":1`, `"stage_count":3`, `"eta_s":16`, `"eta_is_measured":false`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
}

func TestProgressGivesNoEstimateBeforeAnyStep(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, false, false, clk.now)
	p.Begin("x", 10, 5*time.Second, "Working.")
	clk.t = clk.t.Add(time.Second)
	p.Step(0)
	if strings.Contains(b.String(), "left") {
		t.Fatalf("zero steps done gives nothing to extrapolate from: %q", b.String())
	}
}
