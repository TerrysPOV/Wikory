package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestMain lets tests re-execute this binary as a helper process.
func TestMain(m *testing.M) {
	switch os.Getenv("WIKORY_TEST_ROLE") {
	case "lock-holder":
		unlock, err := Lock(os.Getenv("WIKORY_TEST_LOCK"), time.Second)
		if err != nil {
			fmt.Println("lock failed:", err)
			os.Exit(3)
		}
		_ = unlock
		fmt.Println("locked")
		killSelf()
	case "write-lock-holder":
		// Holds the lock until stdin closes, then exits cleanly.
		unlock, err := Lock(os.Getenv("WIKORY_TEST_LOCK"), time.Second)
		if err != nil {
			fmt.Println("lock failed:", err)
			os.Exit(3)
		}
		fmt.Println("locked")
		_, _ = io.Copy(io.Discard, os.Stdin)
		unlock()
		os.Exit(0)
	case "bump-child":
		os.Exit(bumpChild())
	}
	os.Exit(m.Run())
}

// bumpChild is one process of TestParallelProcessesLoseNoCounterWrite: its own Store handle,
// n increments of one shared counter.
func bumpChild() int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := Resolve(ctx, os.Getenv("WIKORY_TEST_DATA"), Identity{ProjectID: "wkp_W", Origin: "github.com/o/w", CommonDir: "/w/.git", Worktree: "/w"}, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve:", err)
		return 1
	}
	defer s.Close()
	n, _ := strconv.Atoi(os.Getenv("WIKORY_TEST_N"))
	for i := 0; i < n; i++ {
		if err := s.Bump(ctx, "parallel"); err != nil {
			fmt.Fprintln(os.Stderr, "bump:", err)
			return 1
		}
	}
	return 0
}
