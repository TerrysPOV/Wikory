package store

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLockExcludesASecondHolder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	u1, err := Lock(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(p, 50*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("second holder: want ErrLockTimeout, got %v", err)
	}
	u1()
	u2, err := Lock(p, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	u2()
}

func TestLockReleasedWhenHolderIsKilled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	exe, _ := os.Executable()
	c := exec.Command(exe, "-test.run=^$")
	c.Env = append(os.Environ(), "WIKORY_TEST_ROLE=lock-holder", "WIKORY_TEST_LOCK="+p)
	out, _ := c.StdoutPipe()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(out).ReadString('\n')
	if line != "locked\n" {
		t.Fatalf("helper did not take the lock: %q", line)
	}
	_ = c.Wait()
	if c.ProcessState.ExitCode() != killedExitCode {
		t.Fatalf("helper was not killed while holding the lock: exit %d", c.ProcessState.ExitCode())
	}
	u, err := Lock(p, time.Second)
	if err != nil {
		t.Fatalf("the OS did not release a dead holder's lock: %v", err)
	}
	u()
}
