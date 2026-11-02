//go:build unix

package app

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO as .git/info/exclude makes `git status` block for ever. Stop must still capture the
// turn inside its budget, and log that the git facts were lost.
func TestStopCapturesWhenGitStatusBlocksOnAFifoExclude(t *testing.T) {
	h := newHookRig(t)
	h.c.extra["WIKORY_HOOK_BUDGET_MS"] = "1200"
	info := filepath.Join(h.dir, ".git", "info")
	_ = os.MkdirAll(info, 0o755)
	_ = os.Remove(filepath.Join(info, "exclude"))
	if err := syscall.Mkfifo(filepath.Join(info, "exclude"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.hook(t, h.dir, "user-prompt-submit", h.promptPayload("x", "p1"))
	_, el, code := runHookTimed(t, h, "stop", h.stopPayload("p1", "done"), 9*time.Second)
	if code != ExitOK || el > 1200*time.Millisecond+500*time.Millisecond*slack() {
		t.Fatalf("code=%d elapsed=%v: Stop outlived its 1200ms budget", code, el)
	}
	if n := h.turnCount(t); n != 1 {
		t.Fatalf("the turn was lost with git status blocked: turns=%d", n)
	}
	if h.logKindCount(t, "git-status") == 0 {
		t.Fatalf("the lost git facts were not logged: %q", h.logText(t))
	}
}

// A stale open marker of another session is stubbed at SessionStart. When that fails, the
// hook logs "sweep" and still briefs.
func TestSweepFailureIsLogged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod does not stop root")
	}
	h := newHookRig(t)
	db := h.storeDB(t)
	if _, err := db.Exec(`INSERT INTO turn_starts(session_id, marker, checkout_id, transcript_path, transcript_offset, request, at)
		VALUES('old-session', 'm1', ?, '', 0, 'old request', '2020-01-01T00:00:00.000000000Z')`, h.checkoutID(t)); err != nil {
		t.Fatal(err)
	}
	turns := filepath.Join(h.storeDir(t), "turns", h.checkoutID(t))
	if err := os.MkdirAll(turns, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(turns, 0o500); err != nil { // the stub cannot be written below this
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(turns, 0o700) })
	if got := h.context(t, h.dir); !strings.HasPrefix(got, "wikory: mode=full") {
		t.Fatalf("a failed sweep stopped the briefing: %s", got)
	}
	if h.logKindCount(t, "sweep") == 0 {
		t.Fatalf("the sweep failure was not logged: %q", h.logText(t))
	}
}

// Doctor says when the log exists but hooks cannot use it, for each way that can happen.
func TestDoctorReportsAnUnusableHookLog(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"symlink": func(t *testing.T, path string) {
			if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere.log"), path); err != nil {
				t.Fatal(err)
			}
		},
		"fifo": func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"unwritable": func(t *testing.T, path string) {
			if os.Geteuid() == 0 {
				t.Skip("chmod does not stop root")
			}
			if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+" stop\n"), 0o400); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, make := range cases {
		t.Run(name, func(t *testing.T) {
			dir, c := initRepoWithPage(t)
			make(t, filepath.Join(c.dataDir, "hook-errors.log"))
			e := doctorFull(t, c, dir)
			if !anyContains(e.Notices, "hook error log") {
				t.Fatalf("an unusable hook error log went unreported: %v", e.Notices)
			}
		})
	}
}
