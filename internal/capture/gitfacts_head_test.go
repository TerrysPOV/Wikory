package capture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/decode"
)

// A branch whose commit does not resolve is a git failure, not an empty revision: the capture
// says so and the record carries an explicit "unknown", never "".
func TestStopRecordsAnUnresolvableHeadAsUnknown(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	branch := strings.TrimSpace(gitOut(t, r.repoDir, "symbolic-ref", "HEAD"))
	ref := filepath.Join(r.repoDir, ".git", filepath.FromSlash(branch))
	if err := os.WriteFile(ref, []byte(strings.Repeat("1", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warned []string
	r.rec.Warn = func(kind string) { warned = append(warned, kind) }
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	id, _, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "done"))
	if err != nil {
		t.Fatal(err)
	}
	if !has(warned, "git") || !strings.Contains(r.readVersion(t, id, "v0001.json"), `"head": "unknown"`) {
		t.Fatalf("warned=%v record=%s", warned, r.readVersion(t, id, "v0001.json"))
	}
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
