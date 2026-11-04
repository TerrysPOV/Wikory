//go:build unix

package app

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func killSelf() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL); select {} }

// diedByTheKill says whether the error Wait returned for a child means that the child was ended
// by SIGKILL. Anything else is not a kill: no error (it exited 0), no error value that carries a
// process state (it never started), a plain exit code, or any other signal. The old test took
// every exit code of -1 for a kill, and -1 is also what ExitCode reports for a child that never
// started and for one that died by SIGTERM, so a helper that never ran passed.
func diedByTheKill(err error) bool {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return false
	}
	ws, ok := ee.ProcessState.Sys().(syscall.WaitStatus)
	return ok && ws.Signaled() && ws.Signal() == syscall.SIGKILL
}

// TestKillClassifierAcceptsOnlyARealSIGKILL pins how strict the kill test is, so the strictness
// cannot regress without this test going red. Each case is a real child process.
func TestKillClassifierAcceptsOnlyARealSIGKILL(t *testing.T) {
	sh := func(script string) error { return exec.Command("sh", "-c", script).Run() }
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"exit 0", sh("exit 0"), false},
		{"plain exit 1", sh("exit 1"), false},
		{"a child that never started", exec.Command("/nonexistent/wikory-kill-test-helper").Run(), false},
		{"SIGTERM", sh("kill -TERM $$"), false},
		{"SIGKILL", sh("kill -KILL $$"), true},
	}
	for _, c := range cases {
		if got := diedByTheKill(c.err); got != c.want {
			t.Errorf("%s: diedByTheKill(%v) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}
