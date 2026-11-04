package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// progressThreshold is the "about two seconds" of spec 9.
const progressThreshold = 2 * time.Second

// Progress applies the binary-wide progress rule. Output goes to stderr.
type Progress struct {
	w        io.Writer
	json     bool
	tty      bool
	now      func() time.Time
	label    string
	notice   string
	total    int
	start    time.Time
	noticed  bool
	lastTick int

	stageName  string
	stageIndex int
	stageCount int
}

// NewProgress builds a reporter. Hooks never create one.
func NewProgress(w io.Writer, jsonMode, tty bool, now func() time.Time) *Progress {
	return &Progress{w: w, json: jsonMode, tty: tty, now: now, lastTick: -1}
}

func (p *Progress) emit(event string, fields map[string]any) {
	if p.json {
		fields["event"] = event
		fields["stage"] = p.label
		if p.stageName != "" {
			fields["stage_name"], fields["stage_index"], fields["stage_count"] = p.stageName, p.stageIndex, p.stageCount
		}
		b, _ := json.Marshal(fields)
		fmt.Fprintln(p.w, string(b))
		return
	}
	switch event {
	case "notice":
		fmt.Fprintln(p.w, "wikory: "+p.notice)
	case "progress":
		line := fmt.Sprintf("wikory: %s %d/%d, %.1fs elapsed", p.label, fields["done"], p.total, fields["elapsed_s"])
		if eta, ok := fields["eta_s"].(float64); ok {
			line += fmt.Sprintf(", about %.0fs left (estimate)", eta)
		}
		if p.tty {
			fmt.Fprint(p.w, "\r"+line)
		} else {
			fmt.Fprintln(p.w, line)
		}
	}
}

// IsTTY reports whether w is an interactive terminal. Anything that is not an *os.File is not
// one, and neither is the null device, which is also a character device.
func IsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return true
}

// BeginStage is Begin for one stage of a multi-stage operation: the label reads
// "stage 2 of 5: inventory" and JSON events carry the stage fields.
func (p *Progress) BeginStage(index, count int, name string, total int, expected time.Duration, notice string) {
	p.stageIndex, p.stageCount, p.stageName = index, count, name
	p.Begin(fmt.Sprintf("stage %d of %d: %s", index, count, name), total, expected, notice)
}

// Begin starts an operation. expected is the estimate; above the threshold the notice prints now.
func (p *Progress) Begin(label string, total int, expected time.Duration, notice string) {
	p.label, p.total, p.notice, p.start, p.noticed, p.lastTick = label, total, notice, p.now(), false, -1
	if expected > progressThreshold {
		p.noticed = true
		p.emit("notice", map[string]any{"notice": notice, "estimate_s": expected.Seconds(), "estimate_is_measured": false})
	}
}

// Step reports progress; it prints only after the notice has been shown.
func (p *Progress) Step(done int) {
	elapsed := p.now().Sub(p.start)
	if !p.noticed && elapsed > progressThreshold {
		p.noticed = true
		p.emit("notice", map[string]any{"notice": p.notice, "elapsed_s": elapsed.Seconds()})
	}
	if !p.noticed || p.total == 0 {
		return
	}
	tick := done * 10 / p.total
	if tick == p.lastTick && !p.tty && !p.json {
		return
	}
	p.lastTick = tick
	fields := map[string]any{"done": done, "total": p.total, "elapsed_s": elapsed.Seconds()}
	if done > 0 && done < p.total {
		eta := elapsed.Seconds() / float64(done) * float64(p.total-done)
		fields["eta_s"], fields["eta_is_measured"] = eta, false
	}
	p.emit("progress", fields)
}

// End finishes the line on a terminal.
func (p *Progress) End() {
	if p.noticed && p.tty && !p.json {
		fmt.Fprintln(p.w)
	}
}
