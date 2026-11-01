//go:build spike

package spikes

import (
	"os"
	"testing"
)

// TestMain lets a spike re-execute this test binary as a child process.
func TestMain(m *testing.M) {
	if os.Getenv("WIKORY_SPIKE_ROLE") == "wal-child" {
		os.Exit(walChild())
	}
	os.Exit(m.Run())
}
