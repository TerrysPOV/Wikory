package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/store"
)

// Another process holds the database write lock for the whole of the hook's budget. Every hook
// that writes must return inside that budget, exit 0, log the kind "dblock" (not a generic
// kind), and leave the write undone. SessionStart writes through the index refresh,
// UserPromptSubmit through the turn-start marker, Stop through the captured turn.
func TestHooksLogDblockWhenTheWriteLockIsHeld(t *testing.T) {
	for _, hc := range allHooks {
		t.Run(hc.arg, func(t *testing.T) {
			h := newHookRig(t)
			h.c.extra["WIKORY_HOOK_BUDGET_MS"] = "600"
			lock := store.WriteLockPath(filepath.Join(h.storeDir(t), "locks"))
			unlock, err := store.Lock(lock, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, el, code := runHookTimed(t, h, hc.arg, h.payload(hc.event, h.dir, hc.extra), 9*time.Second)
			unlock()
			time.Sleep(300 * time.Millisecond) // let any abandoned work see its expired deadline
			if code != ExitOK || el > 600*time.Millisecond+400*time.Millisecond*slack() {
				t.Fatalf("code=%d elapsed=%v: the hook outlived its 600ms budget", code, el)
			}
			if h.logKindCount(t, "dblock") == 0 {
				t.Fatalf("no dblock entry in the hook error log: %q", h.logText(t))
			}
			if hc.arg != "session-start" {
				if n := h.turnCount(t); n != 0 {
					t.Fatalf("the refused write left %d turn row(s)", n)
				}
			}
		})
	}
}
