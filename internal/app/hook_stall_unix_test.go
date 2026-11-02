//go:build unix

package app

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runHookAsync runs the hook in a goroutine with stdin from in and reports how long it took,
// failing if it does not return at all within limit.
func runHookAsync(t *testing.T, h *hookRig, in io.Reader, limit time.Duration) (string, time.Duration) {
	t.Helper()
	var out bytes.Buffer
	env := &Env{IO: IO{In: in, Out: &out, Err: io.Discard}, Version: "test", Cwd: h.dir,
		Getenv: func(k string) string {
			if k == "WIKORY_DATA_DIR" {
				return h.c.dataDir
			}
			if v, ok := h.c.extra[k]; ok {
				return v
			}
			return os.Getenv(k)
		}}
	done := make(chan struct{})
	start := time.Now()
	go func() { RunEnv([]string{"hook", "session-start"}, env); close(done) }()
	select {
	case <-done:
		return out.String(), time.Since(start)
	case <-time.After(limit):
		t.Fatalf("the hook did not return within %v", limit)
		return "", 0
	}
}

func fifoAtHookLog(t *testing.T, h *hookRig) string {
	t.Helper()
	if err := os.MkdirAll(h.c.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(h.c.dataDir, "hook-errors.log")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A FIFO at the error log path (no reader, then a reader that never drains it) must not
// stall the SessionStart timeout path past its budget.
func TestFIFOAtHookErrorLogDoesNotStallSessionStart(t *testing.T) {
	for _, withReader := range []bool{false, true} {
		h := newHookRig(t)
		h.c.extra["WIKORY_HOOK_BUDGET_MS"] = "300"
		p := fifoAtHookLog(t, h)
		var r *os.File
		if withReader {
			var err error
			if r, err = os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0); err != nil {
				t.Fatal(err)
			}
			defer r.Close()
		}
		testHookSlowBriefing = func() { time.Sleep(900 * time.Millisecond) }
		out, el := runHookAsync(t, h, strings.NewReader(h.payload("SessionStart", h.dir, map[string]any{"source": "startup"})), 5*time.Second)
		testHookSlowBriefing = nil
		if el > 700*time.Millisecond*slack() || !strings.Contains(out, "timed out") {
			t.Fatalf("reader=%v: a FIFO at the log path stalled the hook: %v %q", withReader, el, out)
		}
		if r != nil {
			if n, _ := r.Read(make([]byte, 512)); n > 0 {
				t.Fatalf("the hook wrote its error log into a FIFO (%d bytes)", n)
			}
		}
	}
}

// Stdin that never arrives must not stall SessionStart: the budget starts before the read.
func TestStalledStdinDoesNotStallSessionStart(t *testing.T) {
	h := newHookRig(t)
	h.c.extra["WIKORY_HOOK_BUDGET_MS"] = "300"
	pr, pw := io.Pipe()
	defer pw.Close()
	out, el := runHookAsync(t, h, pr, 5*time.Second)
	if el > 700*time.Millisecond*slack() || !strings.Contains(out, "did not arrive") {
		t.Fatalf("a stalled stdin stalled the hook: %v %q", el, out)
	}
}
