//go:build unix

package capture

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/decode"
)

// A FIFO as .git/info/exclude makes `git status` block for ever. Stop is given a deadline:
// it must still capture the turn (without dirty-file facts), return within the deadline, and
// say that the git facts were lost.
func TestStopSurvivesGitStatusBlockedOnAFifoExclude(t *testing.T) {
	r := newRig(t)
	info := filepath.Join(r.repoDir, ".git", "info")
	_ = os.MkdirAll(info, 0o755)
	_ = os.Remove(filepath.Join(info, "exclude"))
	if err := syscall.Mkfifo(filepath.Join(info, "exclude"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.repoDir, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warned []string
	r.rec.Warn = func(kind string) { warned = append(warned, kind) }
	_ = r.rec.TurnStart(context.Background(), r.ev(decode.UserPrompt, "x", "p1", ""))
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	start := time.Now()
	id, created, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "done"))
	el := time.Since(start)
	if err != nil || !created || id == "" {
		t.Fatalf("the turn was not captured: id=%q created=%v err=%v after %v", id, created, err, el)
	}
	if el > 1500*time.Millisecond {
		t.Fatalf("Stop outlived its deadline: %v", el)
	}
	if len(warned) != 1 || warned[0] != "git-status" {
		t.Fatalf("the lost git facts were not reported: %v", warned)
	}
}
