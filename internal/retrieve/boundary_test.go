package retrieve

import (
	"context"
	"strings"
	"testing"
)

// exactPage is a page of exactly n bytes.
func exactPage(n int) string {
	head := "# P\n\n"
	return head + strings.Repeat("x", n-len(head))
}

// The byte cap admits pages up to and including MaxTotalBytes and refuses the first byte over:
// 400 + 600 bytes against a cap of 1000 is admitted whole, 400 + 601 skips the second page.
func TestRefreshByteCapLandsOnTheBoundaryAndOneOver(t *testing.T) {
	for _, tc := range []struct {
		name        string
		second      int
		pages       int
		skipped     int
		wantPartial bool
	}{
		{"exactly at the cap", 600, 2, 0, false},
		{"one byte over the cap", 601, 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			repo := t.TempDir()
			writePage(t, repo, "a.md", exactPage(400))
			writePage(t, repo, "b.md", exactPage(tc.second))
			ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo), MaxTotalBytes: 1000}
			s, err := ix.Refresh(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if s.Pages != tc.pages || s.Skipped != tc.skipped || s.Partial != tc.wantPartial {
				t.Fatalf("pages=%d skipped=%d partial=%v, want %d %d %v (%+v)", s.Pages, s.Skipped, s.Partial, tc.pages, tc.skipped, tc.wantPartial, s)
			}
		})
	}
}

// fits admits a line that reaches the limit exactly and refuses one byte more. The limit is
// the hard character cap, set to the exact size at which the only orientation line still fits.
func TestBriefFitsLineLandsOnTheCapAndOneOver(t *testing.T) {
	in := BriefInput{Mode: "full", LastConsolidated: "never", BudgetTokens: 100000, HardCharCap: DefaultHardCharCap,
		Orientation: []OrientItem{{Type: "decision", Title: "Only", OneLine: "the single item", RelPath: "project-memory/wiki/only.md"}}}
	line := "- decision: Only: the single item\n"
	full := Brief(in)
	at := strings.Index(full, line)
	if at < 0 {
		t.Fatalf("test premise broken: the item line is not in %q", full)
	}
	exact := at + len(line) + len(closing) + moreReserve // the size at which the line fits with nothing to spare

	in.HardCharCap = exact
	out := Brief(in)
	if !strings.Contains(out, line) || len(out) > exact {
		t.Errorf("at the exact cap %d: item shown=%v, output %d bytes", exact, strings.Contains(out, line), len(out))
	}
	in.HardCharCap = exact - 1
	out = Brief(in)
	if strings.Contains(out, line) || !strings.Contains(out, "1 more available.") || len(out) > exact-1 {
		t.Errorf("one byte under the exact cap %d: item shown=%v, output %d bytes:\n%s", exact-1, strings.Contains(out, line), len(out), out)
	}
}
