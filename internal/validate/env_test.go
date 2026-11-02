package validate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/gitx"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestGitEnvWorkBlobIsConfined(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file symlinks need developer mode; confinement itself is covered by internal/confine")
	}
	ctx := context.Background()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	outside := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(outside, []byte("OUTSIDE-SECRET\n"), 0o644)
	gitRun(t, dir, "-c", "init.defaultBranch=main", "init", "-q")
	_ = os.WriteFile(filepath.Join(dir, "q.go"), []byte("package q\n"), 0o644)
	gitRun(t, dir, "add", "q.go")
	gitRun(t, dir, "commit", "-qm", "init")
	if err := os.Symlink(outside, filepath.Join(dir, "link.go")); err != nil {
		t.Fatal(err)
	}
	repo, _ := gitx.Discover(ctx, dir)
	root, _ := confine.Open(dir)
	defer root.Close()
	env := GitEnv{Repo: repo, Root: root, MaxBytes: 1 << 20}
	oid, present, err := env.WorkBlob(ctx, "q.go")
	if err != nil || !present || oid != gitRun(t, dir, "rev-parse", "HEAD:q.go") {
		t.Fatalf("q.go: %s %v %v", oid, present, err)
	}
	if _, present, err := env.WorkBlob(ctx, "link.go"); present || err != nil {
		t.Fatalf("a symlinked source must read as absent: present=%v err=%v", present, err)
	}
}
