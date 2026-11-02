package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/TerrysPOV/Wikory/internal/decode"
)

// capped is the stored length of text cut to n runes: n runes plus the truncation mark.
func capped(n int) int { return n + utf8.RuneCountInString(truncMark) }

func runes(s string) int { return utf8.RuneCountInString(s) }

// Each cap is fed three times its size and checked against the number written here, never the
// constant, so a constant raised by any factor shows in the stored length.
func TestRedactRecordCapsEveryField(t *testing.T) {
	rec := &Record{
		FinalMessage: strings.Repeat("f", 12000),
		Transcript:   Pointer{Rel: strings.Repeat("r", 3000)},
		Git: GitFacts{Branch: strings.Repeat("b", 3000),
			Dirty: []Fingerprint{{Path: strings.Repeat("d", 3000)}}},
		FilesTouched: []string{strings.Repeat("p", 3000)},
		Commands:     []Command{{Text: strings.Repeat("c", 6000)}},
		Verification: []Verification{{Command: strings.Repeat("v", 6000)}},
	}
	redactRecord(rec)
	for name, c := range map[string]struct{ got, want int }{
		"final message":    {runes(rec.FinalMessage), capped(4000)},
		"transcript rel":   {runes(rec.Transcript.Rel), capped(1000)},
		"branch":           {runes(rec.Git.Branch), capped(1000)},
		"dirty path":       {runes(rec.Git.Dirty[0].Path), capped(1000)},
		"file touched":     {runes(rec.FilesTouched[0]), capped(1000)},
		"command":          {runes(rec.Commands[0].Text), capped(2000)},
		"verification cmd": {runes(rec.Verification[0].Command), capped(2000)},
	} {
		if c.got != c.want {
			t.Errorf("%s: stored %d runes, want %d", name, c.got, c.want)
		}
	}
}

// The dirty-file list is cut to maxDirty entries by the capture itself. 120 untracked files
// exceed the cap more than twofold; the cap is written as 50 here, not read from the constant.
func TestStopStoresAtMostMaxDirtyFiles(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	for i := 0; i < 120; i++ {
		name := filepath.Join(r.repoDir, fmt.Sprintf("untracked-%03d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	long := strings.Repeat("a", 12000)
	if err := r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "do A", "p1", "")); err != nil {
		t.Fatal(err)
	}
	id, _, err := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", long))
	if err != nil {
		t.Fatal(err)
	}
	var rec Record
	if err := json.Unmarshal([]byte(r.readVersion(t, id, "v0001.json")), &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Git.Dirty) != 50 {
		t.Errorf("stored %d dirty files, want %d", len(rec.Git.Dirty), 50)
	}
	if got := runes(rec.FinalMessage); got != capped(4000) {
		t.Errorf("stored final message of %d runes, want %d", got, capped(4000))
	}
}
