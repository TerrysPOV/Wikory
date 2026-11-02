package capture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/decode"
)

// One corrupt record must not block the turns behind it: Seal goes on, seals what it can,
// and reports how many it failed on and the first error.
func TestSealContinuesPastACorruptOldestTurn(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "first", "p1", ""))
	id1, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "never written one"))
	r.clk.t = r.clk.t.Add(time.Second) // so created_at orders p1 before p2
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "second", "p2", ""))
	id2, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p2", "never written two"))
	bad := filepath.Join(r.rec.Store.Dir, "turns", r.rec.CheckoutID, id1, "v0001.json")
	if err := os.WriteFile(bad, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.clk.t = r.clk.t.Add(11 * time.Minute) // both are past the seal timeout
	n, err := r.rec.Seal(ctx, r.farDeadline())
	var se *SealError
	if !errors.As(err, &se) || se.Failed != 1 || se.First == nil {
		t.Fatalf("the failure was not reported with its count: n=%d err=%v", n, err)
	}
	state, _, _, _ := r.row(t, id2)
	if n != 1 || state != "sealed" {
		t.Fatalf("a corrupt oldest turn blocked the later one: n=%d state=%s", n, state)
	}
}

func TestSealedIncompleteRecordsSayWhy(t *testing.T) {
	ctx := context.Background()
	big := strings.Repeat("a", 1<<20)
	cases := []struct {
		name, reason string
		setup        func(t *testing.T, r *rig) decode.Event
	}{
		{"outside the approved roots", "outside-root", func(t *testing.T, r *rig) decode.Event {
			ev := r.ev(decode.Stop, "", "p1", "never written")
			ev.TranscriptPath = filepath.Join(t.TempDir(), "elsewhere.jsonl")
			return ev
		}},
		{"unreadable", "unreadable", func(t *testing.T, r *rig) decode.Event {
			_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
			if err := os.Remove(r.transcript); err != nil {
				t.Fatal(err)
			}
			return r.ev(decode.Stop, "", "p1", "never written")
		}},
		{"oversize", "oversize", func(t *testing.T, r *rig) decode.Event {
			_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "x", "p1", ""))
			line := `{"type":"system","pad":"` + big + `"}`
			lines := make([]string, 17)
			for i := range lines {
				lines[i] = line
			}
			r.appendTranscript(t, append(lines, finalLine)...)
			return r.ev(decode.Stop, "", "p1", "Done: all good.")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			ev := c.setup(t, r)
			id, _, err := r.rec.Stop(ctx, ev)
			if err != nil {
				t.Fatal(err)
			}
			r.clk.t = r.clk.t.Add(11 * time.Minute)
			if n, err := r.rec.Seal(ctx, r.farDeadline()); err != nil || n != 1 {
				t.Fatalf("n=%d err=%v", n, err)
			}
			body := r.readVersion(t, id, "v0002.json")
			if !strings.Contains(body, `"incomplete": true`) || !strings.Contains(body, `"incomplete_reason": "`+c.reason+`"`) {
				t.Fatalf("want reason %s: %s", c.reason, body)
			}
			total, byReason, err := r.rec.IncompleteSealed(ctx, 100)
			if err != nil || total != 1 || byReason[c.reason] != 1 {
				t.Fatalf("incomplete count: total=%d by=%v err=%v", total, byReason, err)
			}
		})
	}
}
