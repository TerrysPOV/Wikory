package app

import (
	"bytes"
	"encoding/json"
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

// terminalRow plays writes onto one terminal row: "\r" returns to column 0, "\n" starts a fresh
// row, and every other byte overwrites the cell under the cursor. It returns the last row.
func terminalRow(s string) string {
	var row []byte
	col := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r':
			col = 0
		case '\n':
			row, col = nil, 0
		default:
			if col < len(row) {
				row[col] = s[i]
			} else {
				row = append(row, s[i])
			}
			col++
		}
	}
	return string(row)
}

func TestProgressTTYLineLeavesNoStaleTextWhenTheEstimateShrinks(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, false, true, clk.now)
	p.Begin("scan", 100, 10*time.Second, "Scanning.")
	for _, s := range []struct {
		at   time.Duration
		done int
	}{{time.Second, 1}, {2 * time.Second, 10}, {11 * time.Second, 99}, {12 * time.Second, 100}} {
		clk.t = time.Unix(0, 0).Add(s.at)
		p.Step(s.done)
	}
	got := strings.TrimRight(terminalRow(b.String()), " ")
	if want := "wikory: scan 100/100, 12.0s elapsed"; got != want {
		t.Fatalf("terminal row %q, want %q", got, want)
	}
}

func TestProgressGivesNoEstimateOnceEverythingIsDone(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, false, false, clk.now)
	p.Begin("scan", 10, 10*time.Second, "Scanning.")
	clk.t = clk.t.Add(5 * time.Second)
	p.Step(10)
	if out := b.String(); !strings.Contains(out, "10/10") || strings.Contains(out, "about") {
		t.Fatalf("done == total must carry no estimate: %q", out)
	}
}

func TestProgressBeginResetsTheRememberedLineWidth(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, false, true, clk.now)
	p.Begin("a very long stage label indeed", 10, 10*time.Second, "One.")
	clk.t = clk.t.Add(time.Second)
	p.Step(1)
	p.End()
	b.Reset()
	p.Begin("b", 10, 10*time.Second, "Two.")
	p.Step(5)
	if got := terminalRow(b.String()); got != strings.TrimRight(got, " ") {
		t.Fatalf("the new operation padded to the old line width: %q", got)
	}
}

func TestProgressBeginClearsTheStageFields(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, true, false, clk.now)
	p.BeginStage(1, 3, "scan", 10, 10*time.Second, "Scanning.")
	p.Begin("plain", 10, 10*time.Second, "Plain.")
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"stage_name":"scan"`) {
		t.Fatalf("unexpected notices: %q", b.String())
	}
	for _, key := range []string{"stage_name", "stage_index", "stage_count"} {
		if strings.Contains(lines[1], key) {
			t.Fatalf("second notice carries %s: %s", key, lines[1])
		}
	}
}

func TestProgressJSONLinesStayParseableOnATerminal(t *testing.T) {
	var b bytes.Buffer
	clk := &fakeClock{t: time.Unix(0, 0)}
	p := NewProgress(&b, true, true, clk.now)
	p.BeginStage(1, 2, "scan", 10, 10*time.Second, "Scanning.")
	for i, done := range []int{1, 5, 10} {
		clk.t = time.Unix(0, 0).Add(time.Duration(i+1) * time.Second)
		p.Step(done)
	}
	p.End()
	if strings.Contains(b.String(), "\r") {
		t.Fatalf("carriage return in JSON output: %q", b.String())
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a notice and three events, got %q", b.String())
	}
	for _, l := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Fatalf("line %q does not parse: %v", l, err)
		}
	}
}
