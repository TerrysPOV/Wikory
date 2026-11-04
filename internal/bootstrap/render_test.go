package bootstrap

import (
	"bytes"
	"fmt"
	"slices"
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

// intentModel is a model whose only claims are n intent claims, all bound for Constraints. text
// builds the text of claim i.
func intentModel(t *testing.T, n int, text func(i int) string) RenderModel {
	t.Helper()
	m := renderModel(t)
	m.Claims = nil
	for i := 0; i < n; i++ {
		m.Claims = append(m.Claims, stored(t, "src:repo:docs/decisions/0001.md", "intent", "unverified", text(i), i))
	}
	return m
}

// requireEveryClaimIndexed parses every page with the indexer's own parser and requires that
// each claim of the model is on exactly one page that parses whole: nothing degraded, every
// claim a block, none dropped or doubled.
func requireEveryClaimIndexed(t *testing.T, m RenderModel, pages []PageOut) {
	t.Helper()
	seen := map[string]string{}
	for _, pg := range pages {
		parsed := claim.ParsePage(pg.Path, pg.Content)
		if parsed.Degraded != "" {
			t.Errorf("%s would be indexed in part: %s", pg.Path, parsed.Degraded)
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
	missing := 0
	for _, c := range m.Claims {
		if _, ok := seen[c.ClaimID]; !ok {
			missing++
		}
	}
	if missing > 0 {
		t.Errorf("%d of %d claims are on no page the indexer can read", missing, len(m.Claims))
	}
}

// Review finding 1: a page inside the byte budget can still be refused by the parser's
// structure bounds (more than 2000 backticks, 1500 "[" or "](", 4000 "*" or "_"), which indexes
// it by its title alone. Each model below stays inside the byte budget on every page that a
// byte-only split produces and still passes a bound.
func TestPagesTheParserWouldRefuseForStructureAreSplitUntilItReadsEveryClaim(t *testing.T) {
	cases := map[string]struct {
		n    int
		text func(i int) string
	}{
		"code spans": {400, func(i int) string {
			return fmt.Sprintf("`a%d` calls `b` then `c` and `d` with `e`, `f`, `g` and `h`.", i)
		}},
		"emphasis marks": {300, func(i int) string {
			return fmt.Sprintf("Marks %d %s", i, strings.Repeat("a_", 40))
		}},
		"link marks": {300, func(i int) string {
			return fmt.Sprintf("See %d %s", i, strings.Repeat("[a](b) ", 10))
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := intentModel(t, tc.n, tc.text)
			pages := RenderPages(m)
			requireEveryClaimIndexed(t, m, pages)
			parts := 0
			for _, pg := range pages {
				if strings.Contains(pg.Path, "constraints") {
					parts++
				}
				if len(pg.Content) > MaxPageBytes {
					t.Errorf("%s is %d bytes", pg.Path, len(pg.Content))
				}
			}
			if parts < 2 {
				t.Fatalf("a page the parser refuses must split, got %d part(s)", parts)
			}
			if got := Unrenderable(pages); len(got) != 0 {
				t.Errorf("every claim here can be rendered, got %v", got)
			}
		})
	}
}

// A claim the parser cannot read even alone must neither loop the splitter nor vanish: it is
// placed alone on its own page and named.
func TestAClaimTheParserCannotReadEvenAloneIsPlacedAloneAndReported(t *testing.T) {
	m := intentModel(t, 6, func(i int) string { return fmt.Sprintf("Plain intent number %d.", i) })
	m.Claims[3] = stored(t, "src:repo:docs/decisions/0001.md", "intent", "unverified", strings.Repeat("`a`", 1100), 3)
	bad := m.Claims[3].ClaimID
	pages := RenderPages(m)
	got := Unrenderable(pages)
	if !slices.Equal(got, []string{bad}) {
		t.Fatalf("the unrenderable claim must be reported, got %v want [%s]", got, bad)
	}
	var alone *PageOut
	for i := range pages {
		if strings.Contains(string(pages[i].Content), "c="+bad) {
			alone = &pages[i]
		}
	}
	if alone == nil || strings.Count(string(alone.Content), "<!-- claim ") != 1 || !strings.Contains(string(alone.Content), strings.Repeat("`a`", 1100)) {
		t.Fatalf("the claim is placed alone on a page that still holds its text")
	}
	seen := 0
	for _, pg := range pages {
		parsed := claim.ParsePage(pg.Path, pg.Content)
		if parsed.Degraded != "" && pg.Path != alone.Path {
			t.Errorf("%s would be indexed in part: %s", pg.Path, parsed.Degraded)
		}
		seen += len(parsed.Blocks)
	}
	if seen != 5 {
		t.Fatalf("the five readable claims must all be indexed, got %d", seen)
	}
}

func TestRenderingTwiceGivesTheSamePagesWhateverOrderTheClaimsArriveIn(t *testing.T) {
	m := renderModel(t)
	for i := 2; i < 8; i++ {
		m.Claims = append(m.Claims, stored(t, "src:repo:docs/decisions/0001.md", "intent", "unverified", fmt.Sprintf("Further intent %d.", i), i))
	}
	for i := 0; i < 3; i++ {
		m.Claims = append(m.Claims, stored(t, "src:repo:docs/other.md", "intent", "unverified", fmt.Sprintf("Tied intent %d.", i), 0))
	}
	reversed := m
	reversed.Claims = slices.Clone(m.Claims)
	slices.Reverse(reversed.Claims)
	a, b := RenderPages(m), RenderPages(reversed)
	if len(a) != len(b) {
		t.Fatalf("page count differs: %d and %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Path != b[i].Path || !bytes.Equal(a[i].Content, b[i].Content) {
			t.Fatalf("page %s depends on the order the claims were handed in (production passes them ordered by claim_id)", a[i].Path)
		}
	}
}

func TestComponentsAreRankedHoweverTheCallerOrderedThem(t *testing.T) {
	m := renderModel(t)
	m.Components = []Component{
		{Name: "pkg/a", Prefix: "pkg/a", Rank: 1, Inspected: true, Files: 2},
		{Name: "pkg/b", Prefix: "pkg/b", Rank: 2, Inspected: true, Files: 2},
		{Name: "pkg/c", Prefix: "pkg/c", Rank: 3, Inspected: true, Files: 2},
		{Name: "pkg/d", Prefix: "pkg/d", Rank: 4, Inspected: false, Files: 2},
		{Name: "pkg/e", Prefix: "pkg/e", Rank: 5, Inspected: false, Files: 2},
	}
	m.Claims = []StoredClaim{
		stored(t, "src:repo:docs/design.md", "current-impl", "unverified", "pkg/c is where it ends.", 0),
		stored(t, "src:repo:docs/design.md", "current-impl", "unverified", "pkg/b and pkg/c are siblings.", 1),
	}
	shuffled := m
	shuffled.Components = []Component{m.Components[4], m.Components[2], m.Components[0], m.Components[3], m.Components[1]}
	a, b := RenderPages(m), RenderPages(shuffled)
	if len(a) != len(b) {
		t.Fatalf("page count differs: %d and %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Path != b[i].Path || !bytes.Equal(a[i].Content, b[i].Content) {
			t.Fatalf("page %s depends on the order the components were handed in", a[i].Path)
		}
	}
	var comps []string
	for _, pg := range a {
		if strings.Contains(pg.Path, "/component-") {
			comps = append(comps, pg.Path[strings.LastIndex(pg.Path, "/component-")+1:])
		}
	}
	if len(comps) != 3 || !strings.HasPrefix(comps[0], "component-pkg-a-") || !strings.HasPrefix(comps[1], "component-pkg-b-") || !strings.HasPrefix(comps[2], "component-pkg-c-") {
		t.Fatalf("component pages follow rank: %v", comps)
	}
	// "pkg/b and pkg/c" names two inspected components: the higher-ranked one gets the claim,
	// which only holds if the search runs in rank order.
	if pg := pageByBase(a, "component-pkg-b-"+shortSlugHash("pkg/b")); pg == nil || !strings.Contains(string(pg.Content), "pkg/b and pkg/c are siblings.") {
		t.Fatalf("a claim naming two components goes to the higher-ranked one")
	}
}

func TestEachPartOfASplitPageLinksOnlyToItsFirstPreviousAndNextPart(t *testing.T) {
	ps := pageSpec{base: "constraints", links: []string{"bootstrap-overview"}}
	cases := []struct {
		i    int
		want []string
	}{
		{0, []string{"bootstrap-overview", "bootstrap-constraints-2"}},
		{1, []string{"bootstrap-overview", "bootstrap-constraints", "bootstrap-constraints-3"}},
		{2, []string{"bootstrap-overview", "bootstrap-constraints", "bootstrap-constraints-2", "bootstrap-constraints-4"}},
		{3, []string{"bootstrap-overview", "bootstrap-constraints", "bootstrap-constraints-3"}},
	}
	for _, c := range cases {
		if got := ps.linksFor(c.i, 4); !slices.Equal(got, c.want) {
			t.Errorf("part %d of 4 links to %v, want %v", c.i+1, got, c.want)
		}
	}
}

func TestAClaimAboutAnUninspectedComponentFallsBackToTheGeneralPage(t *testing.T) {
	m := renderModel(t)
	m.Claims = []StoredClaim{stored(t, "comp:internal/other", "current-impl", "unverified", "The other package is not looked at.", 0)}
	pages := RenderPages(m)
	arch := pageByBase(pages, "architecture")
	if arch == nil || !strings.Contains(string(arch.Content), "The other package is not looked at.") {
		t.Fatal("a claim about a component with no page of its own is on the architecture page, not dropped")
	}
	requireEveryClaimIndexed(t, m, pages)
}

func TestAVeryDeepPrefixStillGivesAUsableFileName(t *testing.T) {
	m := renderModel(t)
	deepA := strings.Repeat("deep/", 80) + "pkga"
	deepB := strings.Repeat("deep/", 80) + "pkgb"
	m.Components = []Component{
		{Name: deepA, Prefix: deepA, Rank: 1, Inspected: true, Files: 1},
		{Name: deepB, Prefix: deepB, Rank: 2, Inspected: true, Files: 1},
	}
	m.Claims = []StoredClaim{
		stored(t, "comp:"+deepA, "current-impl", "verified", "The first deep package works.", 0),
		stored(t, "comp:"+deepB, "current-impl", "verified", "The second deep package works.", 0),
	}
	pages := RenderPages(m)
	paths := map[string]bool{}
	for _, pg := range pages {
		base := pg.Path[strings.LastIndex(pg.Path, "/")+1:]
		if len(base) > 255 {
			t.Errorf("file name of %d bytes", len(base))
		}
		if paths[pg.Path] {
			t.Errorf("%s is used twice", pg.Path)
		}
		paths[pg.Path] = true
	}
	requireEveryClaimIndexed(t, m, pages)
}

func TestAComponentNameWithMarkupStillRendersPagesTheParserReadsWhole(t *testing.T) {
	for _, odd := range []string{"a`<b>x", "a`x", "a<b>x", "a<x", "a>x"} {
		m := renderModel(t)
		m.Components = []Component{{Name: odd, Prefix: odd, Rank: 1, Inspected: true, Files: 1}}
		m.Claims = []StoredClaim{stored(t, "comp:"+odd, "current-impl", "verified", "The odd package works.", 0)}
		pages := RenderPages(m)
		for _, pg := range pages {
			parsed := claim.ParsePage(pg.Path, pg.Content)
			if len(parsed.Findings) != 0 || parsed.Degraded != "" {
				t.Errorf("%q: %s: findings %+v, degraded %q", odd, pg.Path, parsed.Findings, parsed.Degraded)
			}
			if bytes.Contains(pg.Content, []byte(odd)) {
				t.Errorf("%q: %s shows the name's markup unchanged", odd, pg.Path)
			}
		}
		requireEveryClaimIndexed(t, m, pages)
	}
}

// A component name is shown only inside a code span, in the title and heading as everywhere else,
// so a name shaped like a link or an image is inert text and the page still parses whole.
func TestAComponentNameShapedLikeALinkRendersInert(t *testing.T) {
	for _, odd := range []string{"[x](http://e)", "![i](http://e/p.png)"} {
		m := renderModel(t)
		m.Components = []Component{{Name: odd, Prefix: odd, Rank: 1, Inspected: true, Files: 1}}
		m.Claims = []StoredClaim{stored(t, "comp:"+odd, "current-impl", "verified", "The odd package works.", 0)}
		for _, pg := range RenderPages(m) {
			parsed := claim.ParsePage(pg.Path, pg.Content)
			if len(parsed.Findings) != 0 || parsed.Degraded != "" {
				t.Errorf("%q: %s: findings %+v, degraded %q", odd, pg.Path, parsed.Findings, parsed.Degraded)
			}
			text := string(pg.Content)
			if strings.Count(text, odd) != strings.Count(text, "`"+odd+"`") {
				t.Errorf("%q: %s shows the name outside a code span:\n%s", odd, pg.Path, text)
			}
		}
	}
}

// The parser check can only halve a part; it does not know the byte budget. A model of about
// 100 KiB of claims would halve into two parts of 50 KiB, each readable and each over budget,
// so the sizes must be packed to the budget first.
func TestPartsAreFilledToTheByteBudgetBeforeTheParserIsAsked(t *testing.T) {
	m := intentModel(t, 0, nil)
	total := 0
	for i := 0; total < 100<<10; i++ {
		c := stored(t, "src:repo:docs/decisions/0001.md", "intent", "unverified", fmt.Sprintf("Intent number %d: %s", i, strings.Repeat("x", 300)), i)
		m.Claims = append(m.Claims, c)
		total += len(c.Meta) + len(c.Text) + 3
	}
	pages := RenderPages(m)
	parts := 0
	for _, pg := range pages {
		if strings.Contains(pg.Path, "constraints") {
			parts++
		}
		if len(pg.Content) > MaxPageBytes {
			t.Errorf("%s is %d bytes, over the %d byte budget", pg.Path, len(pg.Content), MaxPageBytes)
		}
	}
	if parts != 3 {
		t.Errorf("100 KiB of claims fill three parts of the budget, got %d", parts)
	}
	requireEveryClaimIndexed(t, m, pages)
}

func TestOverviewCountsReadAndQueuedSourcesAndNamesWhichItIs(t *testing.T) {
	src := func(d string) Source { return Source{Name: "repo", Path: d + ".md", Tier: TierIntent, Disposition: d} }
	cases := []struct {
		name    string
		sources []Source
		want    string
	}{
		{"queued only", []Source{src(DispQueued), src(DispQueued), src(DispOtherWorktree), src(DispLink)}, "built from 2 source files that were queued (1 more were skipped)"},
		{"read only", []Source{src(DispRead), src(DispRead), src(DispRead), src(DispLink)}, "built from 3 source files that were read (1 more were skipped)"},
		{"both", []Source{src(DispRead), src(DispQueued), src(DispLink)}, "built from 2 source files that were read or queued (1 more were skipped)"},
	}
	for _, c := range cases {
		m := renderModel(t)
		m.Sources = c.sources
		ov := pageByBase(RenderPages(m), "overview")
		if ov == nil || !strings.Contains(string(ov.Content), c.want) {
			t.Errorf("%s: overview does not say %q", c.name, c.want)
		}
	}
}
