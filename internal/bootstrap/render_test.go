package bootstrap

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/ids"
)

// stored builds a StoredClaim whose Meta is a real rendered claim comment.
func stored(t *testing.T, ref, kind, status, text string, ordinal int) StoredClaim {
	t.Helper()
	c := claim.Claim{ID: ids.NewULID(), Kind: kind, Status: status, Scope: "repo", TH: claim.TextHash(text), Author: "agent"}
	meta := c.Render()
	if _, err := claim.ParseComment(meta); err != nil {
		t.Fatal(err)
	}
	return StoredClaim{Item: "itm_" + ref, ClaimID: c.ID, Text: text, Kind: kind, Status: status, Meta: meta, Ref: ref, Ordinal: ordinal, Tier: TierIntent}
}

func renderModel(t *testing.T) RenderModel {
	t.Helper()
	return RenderModel{
		Run:  Run{ID: "run_1", Head: "c0ffee0123456789c0ffee0123456789c0ffee01"},
		Date: "2026-11-03",
		Components: []Component{
			{Name: "internal/queue", Prefix: "internal/queue", Rank: 1, Inspected: true, Files: 3, Entry: true},
			{Name: "internal/other", Prefix: "internal/other", Rank: 2, Inspected: false, Files: 1},
		},
		Sources: sampleResult().Sources,
		Claims: []StoredClaim{
			stored(t, "comp:internal/queue", "current-impl", "verified", "The queue acknowledges after the handler returns.", 0),
			stored(t, "src:repo:docs/design.md", "current-impl", "unverified", "Workers in internal/queue retry five times.", 0),
			stored(t, "src:repo:docs/design.md", "current-impl", "unverified", "Nothing here names a component.", 1),
			stored(t, "src:repo:docs/decisions/0001.md", "intent", "unverified", "Messages are never dropped silently.", 0),
			stored(t, "src:repo:docs/decisions/0001.md", "rationale", "unverified", "At-least-once delivery was chosen for simplicity.", 1),
			stored(t, "src:repo:docs/design.md", "hypothesis", "unverified", "The retry cap may be too low.", 2),
			stored(t, "src:repo:.claude/handoffs/PRIOR_SESSION_STATE.md", "intent", "historical", "Retry work is half done.", 0),
			stored(t, "comp:internal/queue", "current-impl", "conflicting", "The queue acknowledges before the handler runs.", 1),
		},
	}
}

func pageByBase(pages []PageOut, base string) *PageOut {
	for i := range pages {
		if strings.HasSuffix(pages[i].Path, "/"+base+".md") {
			return &pages[i]
		}
	}
	return nil
}

func TestEveryClaimIsRenderedOnExactlyOnePageAndParsesBack(t *testing.T) {
	m := renderModel(t)
	pages := RenderPages(m)
	seen := map[string]string{}
	for _, pg := range pages {
		parsed := claim.ParsePage(pg.Path, pg.Content)
		if parsed.Origin != Origin || !claim.PageTypes[parsed.Type] || parsed.ID == "" || parsed.ID != pg.ID {
			t.Errorf("%s: origin=%q type=%q id=%q (page id %q)", pg.Path, parsed.Origin, parsed.Type, parsed.ID, pg.ID)
		}
		if len(parsed.Findings) != 0 {
			t.Errorf("%s: findings %+v", pg.Path, parsed.Findings)
		}
		for _, b := range parsed.Blocks {
			if b.Err != nil {
				t.Errorf("%s: a rendered claim did not parse: %v", pg.Path, b.Err)
			}
			if prev, dup := seen[b.Claim.ID]; dup {
				t.Errorf("claim %s on both %s and %s", b.Claim.ID, prev, pg.Path)
			}
			seen[b.Claim.ID] = pg.Path
		}
	}
	for _, c := range m.Claims {
		if _, ok := seen[c.ClaimID]; !ok {
			t.Errorf("claim %q was dropped", c.Text)
		}
	}
}

func TestClaimsLandWhereTheRulesSendThem(t *testing.T) {
	m := renderModel(t)
	pages := RenderPages(m)
	where := func(text string) string {
		for _, pg := range pages {
			if bytes.Contains(pg.Content, []byte(text)) && !strings.Contains(pg.Path, "overview") {
				return pg.Path[strings.LastIndex(pg.Path, "/")+1:]
			}
		}
		return ""
	}
	cases := map[string]string{
		"The queue acknowledges after the handler returns.": "component-internal-queue",
		"Workers in internal/queue retry five times.":       "component-internal-queue",
		"Nothing here names a component.":                   "architecture.md",
		"Messages are never dropped silently.":              "constraints.md",
		"At-least-once delivery was chosen for simplicity.": "constraints.md",
		"The retry cap may be too low.":                     "known-issues.md",
		"The queue acknowledges before the handler runs.":   "known-issues.md",
		"Retry work is half done.":                          "unfinished-work.md",
	}
	for text, want := range cases {
		if got := where(text); !strings.HasPrefix(got, want) {
			t.Errorf("%q is on %q, want %q", text, got, want)
		}
	}
}

func TestPagesAreDeterministicAndBannerEveryPage(t *testing.T) {
	m := renderModel(t)
	a, b := RenderPages(m), RenderPages(m)
	if len(a) != len(b) {
		t.Fatal("page count differs")
	}
	for i := range a {
		if !bytes.Equal(a[i].Content, b[i].Content) || a[i].Path != b[i].Path {
			t.Fatalf("page %s differs between two renders of one model", a[i].Path)
		}
		s := string(a[i].Content)
		if !strings.Contains(s, "wikory bootstrap") || !strings.Contains(s, "2026-11-03") || !strings.Contains(s, "c0ffee012345") {
			t.Errorf("%s has no banner naming the tool, date and commit", a[i].Path)
		}
	}
}

func TestEmptySectionsSayNothingWasFoundInsteadOfStayingBlank(t *testing.T) {
	m := renderModel(t)
	m.Claims = nil
	pages := RenderPages(m)
	for _, base := range []string{"constraints", "known-issues", "unfinished-work", "architecture"} {
		pg := pageByBase(pages, base)
		if pg == nil || !strings.Contains(string(pg.Content), "No ") {
			t.Errorf("%s should exist and say what is missing", base)
		}
	}
	comp := pageByBase(pages, "component-internal-queue-"+shortSlugHash("internal/queue"))
	if comp == nil || !strings.Contains(string(comp.Content), "No rationale was found") {
		t.Fatalf("a component page says when rationale is unavailable")
	}
	if pageByBase(pages, "component-internal-other-"+shortSlugHash("internal/other")) != nil {
		t.Fatal("only inspected components get a page")
	}
}

func TestAPageOverTheBudgetSplitsIntoNumberedPagesKeepingHeadings(t *testing.T) {
	m := renderModel(t)
	m.Claims = nil
	for i := 0; i < 300; i++ {
		m.Claims = append(m.Claims, stored(t, "src:repo:docs/decisions/0001.md", "intent", "unverified",
			fmt.Sprintf("Intent number %d: %s", i, strings.Repeat("x", 300)), i))
	}
	pages := RenderPages(m)
	var parts []PageOut
	for _, pg := range pages {
		if strings.Contains(pg.Path, "constraints") {
			parts = append(parts, pg)
		}
	}
	if len(parts) < 2 {
		t.Fatalf("expected a split, got %d page(s)", len(parts))
	}
	total := 0
	for i, pg := range parts {
		if len(pg.Content) > MaxPageBytes+2048 {
			t.Errorf("%s is %d bytes", pg.Path, len(pg.Content))
		}
		parsed := claim.ParsePage(pg.Path, pg.Content)
		total += len(parsed.Blocks)
		if i > 0 && !strings.Contains(string(pg.Content), "## Intent") {
			t.Errorf("a continuation page repeats the section heading so its claims keep a heading")
		}
		if parsed.Degraded != "" {
			t.Errorf("%s would be indexed in part: %s", pg.Path, parsed.Degraded)
		}
	}
	if total != 300 {
		t.Fatalf("all 300 claims must survive the split, got %d", total)
	}
}

func TestEveryRenderedPageFitsTheParseCapWhateverTheComponentCount(t *testing.T) {
	m := renderModel(t)
	m.Components = nil
	// 1500 components: their listing alone is more than 64 KiB if it is left whole.
	for i := 0; i < 1500; i++ {
		p := fmt.Sprintf("internal/generated/package%04d", i)
		m.Components = append(m.Components, Component{Name: p, Prefix: p, Rank: i + 1, Inspected: i < 3, Files: 2})
	}
	m.Claims = nil
	for i := 0; i < 40; i++ {
		m.Claims = append(m.Claims, stored(t, "src:repo:docs/design.md", "current-impl", "unverified",
			fmt.Sprintf("Architecture statement number %d holds.", i), i))
	}
	pages := RenderPages(m)
	total := 0
	for _, pg := range pages {
		if len(pg.Content) > MaxPageBytes {
			t.Errorf("%s is %d bytes; the whole page has to fit the %d byte budget, listing included", pg.Path, len(pg.Content), MaxPageBytes)
		}
		parsed := claim.ParsePage(pg.Path, pg.Content)
		if parsed.Degraded != "" {
			t.Errorf("%s would be indexed in part: %s", pg.Path, parsed.Degraded)
		}
		total += len(parsed.Blocks)
	}
	if total != 40 {
		t.Fatalf("every claim must stay inside the parser's window: %d of 40 survived", total)
	}
	arch := pageByBase(pages, "architecture")
	if arch == nil || !strings.Contains(string(arch.Content), "more components, not listed here") {
		t.Fatal("a cut listing says how much it left out")
	}
}

func TestEverySplitPageIsWithinTheBudgetIncludingItsHeader(t *testing.T) {
	m := renderModel(t)
	m.Claims = nil
	for i := 0; i < 300; i++ {
		m.Claims = append(m.Claims, stored(t, "src:repo:docs/decisions/0001.md", "intent", "unverified",
			fmt.Sprintf("Intent number %d: %s", i, strings.Repeat("x", 300)), i))
	}
	for _, pg := range RenderPages(m) {
		if len(pg.Content) > MaxPageBytes {
			t.Errorf("%s is %d bytes, over the %d byte budget: the head of the page was not counted", pg.Path, len(pg.Content), MaxPageBytes)
		}
	}
}
