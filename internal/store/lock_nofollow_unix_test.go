//go:build unix

package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The lock file is opened without following a final symlink and is checked to be a regular
// file, so a planted link or FIFO cannot redirect or stall a lock.
func TestLockRefusesASymlinkedLockFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "x.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if unlock, err := Lock(link, 50*time.Millisecond); err == nil {
		unlock()
		t.Fatal("Lock followed a symlink and locked its target")
	}
}

func TestLockRefusesANonRegularLockFile(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "x.lock")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFOs here: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		unlock, err := Lock(fifo, 50*time.Millisecond)
		if err == nil {
			unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Lock accepted a FIFO as its lock file")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Lock blocked on a FIFO")
	}
}

func TestLockRefusesADeviceAsALockFile(t *testing.T) {
	if _, err := os.Stat(os.DevNull); err != nil {
		t.Skip("no " + os.DevNull)
	}
	if unlock, err := Lock(os.DevNull, 50*time.Millisecond); err == nil {
		unlock()
		t.Fatal("Lock accepted a device as its lock file")
	}
}
