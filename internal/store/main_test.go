package store

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestMain lets tests re-execute this binary as a helper process.
func TestMain(m *testing.M) {
	if os.Getenv("WIKORY_TEST_ROLE") == "lock-holder" {
		unlock, err := Lock(os.Getenv("WIKORY_TEST_LOCK"), time.Second)
		if err != nil {
			fmt.Println("lock failed:", err)
			os.Exit(3)
		}
		_ = unlock
		fmt.Println("locked")
		killSelf()
	}
	os.Exit(m.Run())
}
