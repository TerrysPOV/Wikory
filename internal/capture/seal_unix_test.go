//go:build unix

package capture

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/decode"
)

func TestSymlinkedTranscriptIsRefused(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	outside := filepath.Join(t.TempDir(), "elsewhere.jsonl")
	_ = os.WriteFile(outside, []byte(bashUse+"\n"+bashResult+"\n"+finalLine+"\n"), 0o600)
	link := filepath.Join(filepath.Dir(r.transcript), "linked.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	r.transcript = link
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "Done: all good."))
	r.clk.t = r.clk.t.Add(11 * time.Minute)
	_, _ = r.rec.Seal(ctx, time.Now().Add(time.Second))
	if body := r.readVersion(t, id, "v0002.json"); strings.Contains(body, "go test") || !strings.Contains(body, `"incomplete": true`) {
		t.Fatalf("a symlinked transcript was followed: %s", body)
	}
}
