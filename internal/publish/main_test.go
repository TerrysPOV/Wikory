package publish

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/store"
)

func TestMain(m *testing.M) {
	if os.Getenv("WIKORY_TEST_ROLE") == "publish-helper" {
		os.Exit(publishHelper())
	}
	os.Exit(m.Run())
}

// publishHelper begins and publishes the standard ops, dying at WIKORY_KILL_AT.
func publishHelper() int {
	at := os.Getenv("WIKORY_KILL_AT")
	killHook = func(step string) {
		if step == at {
			killSelf()
		}
		if strings.HasSuffix(at, "-1") && step == "dbmarked-0" { // the next replace is op 1's
			confine.SetStepHook(func(s string) {
				if s+"-1" == at {
					killSelf()
				}
			})
		}
	}
	ctx := context.Background()
	st, err := store.Resolve(ctx, os.Getenv("WIKORY_TEST_DATA"), store.Identity{ProjectID: "wkp_P", CommonDir: "/c/.git", Worktree: "/c"}, false)
	if err != nil {
		fmt.Println("resolve:", err)
		return 3
	}
	repo, err := confine.Open(os.Getenv("WIKORY_TEST_REPO"))
	if err != nil {
		fmt.Println("repo:", err)
		return 3
	}
	p := &Publisher{DB: st.DB, Store: st.Root, Repo: repo, CheckoutID: "co-1",
		LockPath: st.Dir + "/locks/publish.lock", LogPath: "project-memory/log.md",
		Fingerprint: func(context.Context) (string, error) { return "tree-1", nil }, AbandonAfter: time.Nanosecond, Now: time.Now}
	j, err := p.Begin(ctx, "test")
	if err != nil {
		fmt.Println("begin:", err)
		return 3
	}
	if err := p.Publish(ctx, j, standardOps(), "kill test"); err != nil {
		fmt.Println("publish:", err)
		return 4
	}
	return 0 // reached only if the kill step was never hit
}
