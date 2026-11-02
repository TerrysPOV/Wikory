package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/publish"
)

// init stopped after it published the config and before it published the other files. Running
// init again must finish the job, not collect the missing files against a stale base and fail.
func TestInitRetryFinishesAnInterruptedInit(t *testing.T) {
	dir := newGitRepo(t)
	c := newCLI(t)

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		publish.SetKillHook(func(step string) {
			if step == "applied-0" {
				runtime.Goexit()
			}
		})
		c.run(dir, "init")
	}()
	<-stopped
	publish.SetKillHook(nil)

	readme := filepath.Join(dir, "project-memory", "wiki", "README.md")
	if _, err := os.Stat(filepath.Join(dir, "project-memory", "config.json")); err != nil {
		if _, err2 := os.Stat(filepath.Join(dir, "project-memory")); err2 != nil {
			t.Fatalf("test premise broken: init stopped before it published anything: %v", err)
		}
	}
	if _, err := os.Stat(readme); err == nil {
		t.Fatal("test premise broken: init finished, so there is no interrupted job")
	}

	out, errOut, code := c.run(dir, "init")
	if code != ExitOK {
		t.Fatalf("retry exit %d: %s %s", code, out, errOut)
	}
	if _, err := os.Stat(readme); err != nil {
		t.Fatalf("the retry did not finish the interrupted init: %v", err)
	}
}
