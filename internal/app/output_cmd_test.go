package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// brokenPipe is a stdout whose reader has gone away.
type brokenPipe struct{}

func (brokenPipe) Write([]byte) (int, error) { return 0, errors.New("write: broken pipe") }

// A --json answer that cannot be written (the caller closed its end) used to be dropped without
// a trace. The command's own outcome is unchanged: success still exits 0 and a failure keeps
// its failure code, but the lost output is logged as kind "output" for doctor to count.
func TestJSONEnvelopeWriteFailureIsLoggedAndKeepsTheExitCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"a successful command", []string{"--json", "recover"}, ExitOK},
		{"a failing command", []string{"--json", "search"}, ExitUsage}, // no terms: a usage error
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, c := initRepoWithPage(t)
			env := testEnv(c, dir)
			env.IO.Out = brokenPipe{}
			code := RunEnv(tc.args, env)
			if code != tc.want {
				t.Fatalf("exit code %d, want %d", code, tc.want)
			}
			b, err := os.ReadFile(filepath.Join(c.dataDir, "hook-errors.log"))
			if err != nil || !strings.Contains(string(b), " output\n") {
				t.Fatalf("the lost JSON output was not logged as kind output: %q (%v)", b, err)
			}
		})
	}
}

// With a working stdout nothing is logged.
func TestJSONEnvelopeWriteSuccessLogsNothing(t *testing.T) {
	dir, c := initRepoWithPage(t)
	if _, _, code := c.run(dir, "--json", "recover"); code != ExitOK {
		t.Fatalf("exit code %d", code)
	}
	if b, err := os.ReadFile(filepath.Join(c.dataDir, "hook-errors.log")); err == nil && strings.Contains(string(b), "output") {
		t.Fatalf("an output failure was logged although stdout worked: %q", b)
	}
}
