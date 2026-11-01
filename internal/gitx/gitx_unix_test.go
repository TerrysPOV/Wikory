//go:build unix

package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryFsmonitorIsNeverRun(t *testing.T) {
	dir := newRepo(t)
	marker := filepath.Join(dir, "PWNED")
	script := filepath.Join(dir, "fsmon.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "config", "core.fsmonitor", script)
	r, _ := Discover(context.Background(), dir)
	if _, err := r.Dirty(context.Background(), "."); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's core.fsmonitor command ran")
	}
}
