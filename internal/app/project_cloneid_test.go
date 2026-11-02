package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/store"
)

// Review L3: ensureCloneID rebinds the store and then writes the nonce file. Two inits of the
// same clone used to interleave those steps, leaving the store and the file naming different
// ids (which fails closed, but destroys the clone's data again on every later init).
func TestConcurrentCloneIdSetupsLeaveStoreAndFileAgreeing(t *testing.T) {
	ctx := context.Background()
	commonDir, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.MkdirAll(commonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := store.Identity{ProjectID: "wkp_P", CommonDir: commonDir, Worktree: filepath.Dir(commonDir)}
	st, err := store.Resolve(ctx, t.TempDir(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// The first goroutine to finish its rebind waits until a second one reaches the start of
	// its own rebind (or, if the lock keeps it out, for half a second), so without a lock the
	// two interleave deterministically.
	var starts, firstDone int32
	second := make(chan struct{})
	testHookCloneID = func(stage string) error {
		switch stage {
		case "before-revoke":
			if atomic.AddInt32(&starts, 1) == 2 {
				close(second)
			}
		case "after-revoke":
			if atomic.CompareAndSwapInt32(&firstDone, 0, 1) {
				select {
				case <-second:
				case <-time.After(500 * time.Millisecond):
				}
			}
		}
		return nil
	}
	t.Cleanup(func() { testHookCloneID = nil })

	var wg sync.WaitGroup
	got := make([]string, 2)
	errs := make([]error, 2)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _, errs[i] = ensureCloneID(ctx, st, commonDir)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("setup %d: %v", i, err)
		}
	}
	inStore, ok, err := st.CloneID(ctx, commonDir)
	if err != nil || !ok {
		t.Fatalf("store clone id: %q %v %v", inStore, ok, err)
	}
	if inFile := readCloneID(commonDir); inFile != inStore {
		t.Fatalf("the store records %q but the git directory carries %q", inStore, inFile)
	}
	if got[0] != inStore || got[1] != inStore {
		t.Fatalf("the two setups returned %q and %q, but the clone's id is %q", got[0], got[1], inStore)
	}
}
