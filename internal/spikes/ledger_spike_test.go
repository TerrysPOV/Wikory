//go:build spike && unix

package spikes

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// take runs ledger_take from docs/spikes/lib.sh against a private scratch area. The scratch
// directory is the test's own temporary directory, so the real ledger is never touched.
func take(t *testing.T, scratch string) (string, int) {
	t.Helper()
	lib, err := filepath.Abs("../../docs/spikes/lib.sh")
	must(t, err)
	c := exec.Command("bash", "-c", `. "$1"; ledger_take claude probe test`, "bash", lib)
	c.Env = append(os.Environ(), "WIKORY_SPIKE_SCRATCH="+scratch)
	out, _ := c.CombinedOutput()
	return strings.TrimSpace(string(out)), c.ProcessState.ExitCode()
}

// The launch ledger is enforced: twelve takes succeed and the thirteenth is refused with
// exit 3.
func TestLedgerRefusesTheThirteenthLaunch(t *testing.T) {
	scratch := t.TempDir()
	for i := 1; i <= 12; i++ {
		if out, code := take(t, scratch); code != 0 || out != strconv.Itoa(i) {
			t.Fatalf("take %d: exit %d %q", i, code, out)
		}
	}
	if out, code := take(t, scratch); code != 3 || !strings.Contains(out, "budget exhausted") {
		t.Fatalf("the 13th launch was not refused: exit %d %q", code, out)
	}
}

// Parallel takes never record more than twelve launches.
func TestLedgerHoldsUnderParallelTakes(t *testing.T) {
	scratch := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); take(t, scratch) }()
	}
	wg.Wait()
	data, _ := os.ReadFile(filepath.Join(scratch, "wikory-spikes", "ledger.tsv"))
	if n := strings.Count(string(data), "\tlaunched\n"); n != 12 {
		t.Fatalf("parallel takes recorded %d launches, want 12", n)
	}
}
