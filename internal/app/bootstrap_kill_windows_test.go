//go:build windows

// The Windows leg of the kill test has been compiled and vetted (GOOS=windows go vet and go test
// -c) but never run: no Windows machine was available when it was written. A kill there is
// os.Exit(77), which runs no deferred call but is not a signal. Run it on windows-latest in CI
// before relying on it.

package app

import (
	"errors"
	"os"
	"os/exec"
	"testing"
)

const killedExitCode = 77

func killSelf() { os.Exit(killedExitCode) }

// diedByTheKill says whether the error Wait returned for a child means that the child exited
// with the code killSelf uses. No error, a child that never started and any other exit code are
// not a kill.
func diedByTheKill(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == killedExitCode
}

// TestKillClassifierAcceptsOnlyARealSIGKILL pins how strict the kill test is. The name is shared
// with the unix leg so that one exit check names both; here the kill is exit code 77.
func TestKillClassifierAcceptsOnlyARealSIGKILL(t *testing.T) {
	cmd := func(script string) error { return exec.Command("cmd", "/c", script).Run() }
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"no error", nil, false},
		{"exit 0", cmd("exit 0"), false},
		{"plain exit 1", cmd("exit 1"), false},
		{"a child that never started", exec.Command(`C:\nonexistent\wikory-kill-test-helper.exe`).Run(), false},
		{"exit 78", cmd("exit 78"), false},
		{"exit 77", cmd("exit 77"), true},
	}
	for _, c := range cases {
		if got := diedByTheKill(c.err); got != c.want {
			t.Errorf("%s: diedByTheKill(%v) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}
