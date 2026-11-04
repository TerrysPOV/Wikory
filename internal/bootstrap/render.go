package bootstrap

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/ids"
)

const (
	WikiDir = "project-memory/wiki/bootstrap"
	// MaxPageBytes is the budget for a whole page: frontmatter, title, banner, introduction,
	// links and claims. The indexer parses 64 KiB, so a page inside this budget is parsed whole.
	MaxPageBytes = 48 << 10
	// MaxListedBytes is the most a component listing may take of the architecture page.
	MaxListedBytes = 16 << 10
	// minClaimBudget keeps a page from splitting into one claim per part if its head is huge.
	minClaimBudget = 4 << 10
	// maxSlugBytes bounds the readable part of a component page's file name, so a deep prefix
	// cannot push the name past the 255 bytes a file system allows. The hash that follows it
	// keeps two cut prefixes apart.
	maxSlugBytes = 80
	Origin       = "bootstrap"
)

// RenderModel is everything the templates need. Date comes from the run, not the clock, so a
// page rendered twice from one model is byte-identical.
type RenderModel struct {
	Run        Run
	Date       string
	Components []Component
	Sources    []Source
	Claims     []StoredClaim
}

// PageOut is one rendered page. Unrenderable names the claims on it that the indexer's parser
// still cannot see (a single claim whose own text is too complex or hides text), each placed
// alone on its page. The caller must report them: the page holds them, the index does not.
type PageOut struct {
	Path, ID     string
	Content      []byte
	Unrenderable []string
}

// Unrenderable lists the claim ids that some page holds but the indexer cannot parse, in page
// order.
func Unrenderable(pages []PageOut) []string {
	var out []string
	for _, pg := range pages {
		out = append(out, pg.Unrenderable...)
	}
	return out
}

type item struct {
	heading string
	text    string
	claimID string
}

type section struct {
	heading string
	claims  []StoredClaim
}

type pageSpec struct {
	base  string
	typ   string
	title string
	intro []string
	secs  []section
	empty string
	links []string
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// shortSlugHash disambiguates two prefixes that slug to the same text.
func shortSlugHash(prefix string) string { return ids.SHA256Hex([]byte(prefix))[:6] }

func componentBase(prefix string) string {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(prefix), "-"), "-")
	if slug == "" {
		slug = "root"
	}
	if len(slug) > maxSlugBytes {
		slug = strings.TrimRight(slug[:maxSlugBytes], "-")
	}
	return "component-" + slug + "-" + shortSlugHash(prefix)
}

// displayName is a component name as it is shown in a title, heading or listing. A backtick,
// "<" or ">" in a name would open a code span or read as inline HTML and the indexer would
// leave that heading out, so each shows as a harmless look-alike. The real prefix stays in the
// claim-bearing references and in the file name's hash.
var displayName = strings.NewReplacer("`", "'", "<", "\u2039", ">", "\u203a").Replace

var statusOrder = []string{"verified", "conflicting", "unverified", "historical", "superseded"}

var statusHeading = map[string]string{
	"verified":    "Verified against code",
	"conflicting": "Conflicting",
	"unverified":  "Not yet checked against code",
	"historical":  "Historical (describes the past)",
	"superseded":  "Superseded",
}

func byStatus(cs []StoredClaim) []section {
	var out []section
	for _, st := range statusOrder {
		var group []StoredClaim
		for _, c := range cs {
			if c.Status == st {
				group = append(group, c)
			}
		}
		if len(group) > 0 {
			out = append(out, section{heading: statusHeading[st], claims: group})
		}
	}
	return out
}

func outsideOrHandoff(ref string) bool {
	name, p, ok := splitSrcRef(ref)
	return ok && (name != "repo" || IsHandoff(p))
}

// byRank is the components in rank order, whatever order the caller held them in.
func byRank(cs []Component) []Component {
	out := append([]Component(nil), cs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		return out[i].Prefix < out[j].Prefix
	})
	return out
}

// claimOwners maps each claim id to the inspected component that owns it (AssignClaims), so a
// claim sits on the page of the component whose verifier judged it and that the coverage report
// credits. A claim no inspected component owns maps to nothing and goes on the general page.
func claimOwners(comps []Component, claims []StoredClaim) map[string]string {
	out := map[string]string{}
	for prefix, list := range AssignClaims(comps, claims) {
		for _, c := range list {
			out[c.ClaimID] = prefix
		}
	}
	return out
}

type placement struct {
	intent, rationale, conflicting, hypotheses, unfinished, general []StoredClaim
	byComponent                                                     map[string][]StoredClaim
}

func place(m RenderModel) placement {
	pl := placement{byComponent: map[string][]StoredClaim{}}
	owners := claimOwners(m.Components, m.Claims)
	cs := append([]StoredClaim(nil), m.Claims...)
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		if a.Ordinal != b.Ordinal {
			return a.Ordinal < b.Ordinal
		}
		return a.ClaimID < b.ClaimID
	})
	for _, c := range cs {
		switch {
		case c.Status == "conflicting":
			pl.conflicting = append(pl.conflicting, c)
		case outsideOrHandoff(c.Ref):
			pl.unfinished = append(pl.unfinished, c)
		case c.Kind == "intent":
			pl.intent = append(pl.intent, c)
		case c.Kind == "rationale":
			pl.rationale = append(pl.rationale, c)
		case c.Kind == "hypothesis":
			pl.hypotheses = append(pl.hypotheses, c)
		default:
			if comp := owners[c.ClaimID]; comp != "" {
				pl.byComponent[comp] = append(pl.byComponent[comp], c)
			} else {
				pl.general = append(pl.general, c)
			}
		}
	}
	return pl
}

// RenderPages builds the baseline pages from a model. It writes no prose of its own beyond
// fixed template sentences.
func RenderPages(m RenderModel) []PageOut {
	pl := place(m)
	head12 := m.Run.Head
	if len(head12) > 12 {
		head12 = head12[:12]
	}
	banner := fmt.Sprintf("> This page is a baseline built by `wikory bootstrap` on %s from commit %s. It was assembled by a fixed template from the project's code, documents and notes; none of its prose was written by hand. Each claim keeps the status it earned: verified claims were checked against committed code, unverified claims were not, and historical claims describe the past. Edit or delete anything: Wikory will not overwrite a page you changed.", m.Date, head12)

	counts := map[string]int{}
	for _, c := range m.Claims {
		counts[c.Status]++
	}
	read, queued, skipped := 0, 0, 0
	for _, s := range m.Sources {
		switch s.Disposition {
		case DispQueued:
			queued++
		case DispRead:
			read++
		case DispUnmergedBranch, DispOtherWorktree: // a branch or worktree is not a source file
		default:
			skipped++
		}
	}
	var inspected, rest []Component
	for _, c := range byRank(m.Components) {
		if c.Inspected {
			inspected = append(inspected, c)
		} else {
			rest = append(rest, c)
		}
	}

	var specs []pageSpec
	var compBases []string
	for _, c := range inspected {
		compBases = append(compBases, "bootstrap-"+componentBase(c.Prefix))
	}
	specs = append(specs, pageSpec{base: "overview", typ: "concept", title: "Project overview (bootstrap baseline)",
		intro: []string{
			fmt.Sprintf("This section is a starting point for an existing project. It was built from %d source files that were %s (%d more were skipped) and holds %d claims: %d verified, %d conflicting, %d not yet checked and %d historical.",
				read+queued, sourceVerb(read, queued), skipped, len(m.Claims), counts["verified"], counts["conflicting"], counts["unverified"], counts["historical"]),
			"- `bootstrap-architecture`: the components found and how they were ranked\n- `bootstrap-constraints`: stated intent, constraints and rationale\n- `bootstrap-known-issues`: open questions and conflicting claims\n- `bootstrap-unfinished-work`: what handoffs and notes say was in progress",
		},
		links: append([]string{"bootstrap-architecture", "bootstrap-constraints", "bootstrap-known-issues", "bootstrap-unfinished-work"}, compBases...)})

	var archIntro []string
	archIntro = append(archIntro, fmt.Sprintf("%d components were found. They were ranked by recent change, by how often documents and handoffs name them, and by whether they are entry points. The first %d were checked against code.", len(m.Components), len(inspected)))
	var lines []string
	for _, c := range inspected {
		entry := ""
		if c.Entry {
			entry = ", entry point"
		}
		lines = append(lines, fmt.Sprintf("- `%s`: rank %d, %d files%s, checked against code", displayName(c.Prefix), c.Rank, c.Files, entry))
	}
	for _, c := range rest {
		lines = append(lines, fmt.Sprintf("- `%s`: rank %d, %d files, not checked; claims about it stay unverified", displayName(c.Prefix), c.Rank, c.Files))
	}
	if len(lines) > 0 {
		archIntro = append(archIntro, capListing(lines, MaxListedBytes, "components"))
	}
	specs = append(specs, pageSpec{base: "architecture", typ: "concept", title: "Architecture (bootstrap baseline)", intro: archIntro,
		secs: byStatus(pl.general), empty: "No implementation claims outside a component page were found.", links: append([]string{"bootstrap-overview"}, compBases...)})

	specs = append(specs, pageSpec{base: "constraints", typ: "concept", title: "Constraints and intent (bootstrap baseline)",
		intro: []string{"What the project's own documents say it intends. These are statements, not checks."},
		secs:  []section{{heading: "Intent", claims: pl.intent}, {heading: "Rationale", claims: pl.rationale}},
		empty: "No intent or rationale claims were found in the sources.", links: []string{"bootstrap-overview"}})

	specs = append(specs, pageSpec{base: "known-issues", typ: "concept", title: "Known issues (bootstrap baseline)",
		intro: []string{"Claims that contradict each other, and open questions the sources raised."},
		secs:  []section{{heading: "Conflicting", claims: pl.conflicting}, {heading: "Open questions", claims: pl.hypotheses}},
		empty: "No conflicting claims or open questions were found.", links: []string{"bootstrap-overview"}})

	specs = append(specs, pageSpec{base: "unfinished-work", typ: "concept", title: "Unfinished work (bootstrap baseline)",
		intro: []string{"What handoffs and notes outside the code say was in progress. They describe the past and have not been checked."},
		secs:  byStatus(pl.unfinished), empty: "No handoff or note claims were found.", links: []string{"bootstrap-overview"}})

	for _, c := range inspected {
		cs := pl.byComponent[c.Prefix]
		secs := byStatus(cs)
		hasRationale := false
		for _, x := range cs {
			hasRationale = hasRationale || x.Kind == "rationale"
		}
		entry := ""
		if c.Entry {
			entry = ", entry point"
		}
		intro := []string{fmt.Sprintf("Component `%s`: rank %d of %d, %d files%s.", displayName(c.Prefix), c.Rank, len(m.Components), c.Files, entry)}
		if !hasRationale {
			intro = append(intro, "No rationale was found for this component in the sources.")
		}
		specs = append(specs, pageSpec{base: componentBase(c.Prefix), typ: "component", title: "Component: " + displayName(c.Prefix), intro: intro, secs: secs,
			empty: "No implementation claims were extracted for this component.", links: []string{"bootstrap-overview", "bootstrap-architecture"}})
	}

	var out []PageOut
	for _, sp := range specs {
		out = append(out, sp.pages(banner)...)
	}
	return out
}

// sourceVerb says what happened to the counted sources. Inventory only queues a source; a
// source is "read" only if something recorded that it was.
func sourceVerb(read, queued int) string {
	switch {
	case queued == 0:
		return "read"
	case read == 0:
		return "queued"
	}
	return "read or queued"
}

// capListing joins lines but stops before maxBytes and says how many it left out, so a project
// with thousands of components cannot push its claims off the page.
func capListing(lines []string, maxBytes int, what string) string {
	used := 0
	for i, l := range lines {
		if used+len(l)+1 > maxBytes {
			return strings.TrimLeft(strings.Join(lines[:i], "\n")+fmt.Sprintf("\n- and %d more %s, not listed here (the coverage report names the ones that were not checked)", len(lines)-i, what), "\n")
		}
		used += len(l) + 1
	}
	return strings.Join(lines, "\n")
}

func (ps pageSpec) idOf(i int) string {
	if i == 0 {
		return "bootstrap-" + ps.base
	}
	return "bootstrap-" + ps.base + "-" + strconv.Itoa(i+1)
}

// linksFor is the spec's own links plus the first, previous and next part of a split page, so
// the link list does not grow with the number of parts.
func (ps pageSpec) linksFor(i, parts int) []string {
	links := append([]string(nil), ps.links...)
	for _, j := range []int{0, i - 1, i + 1} {
		if j >= 0 && j < parts && j != i && !slices.Contains(links, ps.idOf(j)) {
			links = append(links, ps.idOf(j))
		}
	}
	return links
}

// head is everything on a page before its first claim: frontmatter, title, banner and, on the
// first part, the introduction.
func (ps pageSpec) head(banner string, i int, links []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %s\ntype: %s\ntitle: %s\norigin: %s\nlinks: [%s]\n---\n", ps.idOf(i), ps.typ, ps.title, Origin, strings.Join(links, ", "))
	title := ps.title
	if i > 0 {
		title += " (part " + strconv.Itoa(i+1) + ")"
	}
	fmt.Fprintf(&b, "# %s\n\n%s\n\n", title, banner)
	if i == 0 {
		for _, p := range ps.intro {
			b.WriteString(p + "\n\n")
		}
	}
	return b.String()
}

// itemsOf is the claims of a page in order, each with the heading it sits under.
func (ps pageSpec) itemsOf() []item {
	var items []item
	for _, s := range ps.secs {
		for _, c := range s.claims {
			items = append(items, item{heading: s.heading, text: c.Meta + "\n" + c.Text + "\n\n", claimID: c.ClaimID})
		}
	}
	return items
}

// body writes the claims of one part. A part starts with the heading of its first claim, so a
// continuation page keeps every claim under a heading.
func (ps pageSpec) body(g []item) string {
	if len(g) == 0 {
		return ps.empty + "\n\n"
	}
	var b strings.Builder
	prev := ""
	for _, it := range g {
		if it.heading != prev {
			b.WriteString("## " + it.heading + "\n\n")
			prev = it.heading
		}
		b.WriteString(it.text)
	}
	return b.String()
}

func (ps pageSpec) pageFile(i int) string {
	return strings.TrimPrefix(ps.idOf(i), "bootstrap-") + ".md"
}

func (ps pageSpec) content(banner string, i, parts int, g []item) []byte {
	return []byte(ps.head(banner, i, ps.linksFor(i, parts)) + ps.body(g))
}

// sound reports whether the indexer's own parser sees every claim of a part: the page is not
// degraded and it yields exactly the claims placed on it, each of which parses.
func (ps pageSpec) sound(banner string, i, parts int, g []item) bool {
	pg := claim.ParsePage(WikiDir+"/"+ps.pageFile(i), ps.content(banner, i, parts, g))
	if pg.Degraded != "" || len(pg.Blocks) != len(g) {
		return false
	}
	for _, b := range pg.Blocks {
		if b.Err != nil {
			return false
		}
	}
	return true
}

// headingCost is what a heading adds to a part that starts a section with it.
func headingCost(it item) int { return len("## ") + len(it.heading) + len("\n\n") }

// group packs claims into parts by size alone. The parser has limits that bytes do not show
// (code spans, links and emphasis marks per page), so pages() checks each part afterwards.
func group(items []item, budget int) [][]item {
	var groups [][]item
	var cur []item
	size := 0
	for _, it := range items {
		cost := len(it.text)
		if len(cur) == 0 || cur[len(cur)-1].heading != it.heading {
			cost += headingCost(it)
		}
		if size+cost > budget && len(cur) > 0 {
			groups = append(groups, cur)
			cur, size, cost = nil, 0, len(it.text)+headingCost(it)
		}
		cur = append(cur, it)
		size += cost
	}
	if len(cur) > 0 || len(groups) == 0 {
		groups = append(groups, cur)
	}
	return groups
}

func (ps pageSpec) pages(banner string) []PageOut {
	items := ps.itemsOf()
	// The whole page has to fit MaxPageBytes, not just its claims. What claims may use is what
	// the widest head leaves: the first part, with its introduction, a full link list and a
	// long part number (the slack covers " (part 99999)" in a later title).
	worst := append(append([]string(nil), ps.links...), ps.idOf(99999), ps.idOf(99998), ps.idOf(99997))
	budget := MaxPageBytes - len(ps.head(banner, 0, worst)) - 32
	if budget < minClaimBudget {
		budget = minClaimBudget
	}
	groups := group(items, budget)
	// Verify each part with the real parser and halve any that it does not see whole. A part
	// that holds a single claim and still fails cannot be helped by splitting: it stays alone
	// on its page and is reported, never dropped.
	var bad []string // claim ids, in page order
	for i := 0; i < len(groups); {
		g := groups[i]
		switch {
		case len(g) == 0 || ps.sound(banner, i, len(groups), g):
			i++
		case len(g) == 1:
			bad = append(bad, g[0].claimID)
			i++
		default:
			mid := len(g) / 2
			groups = slices.Insert(groups, i+1, slices.Clone(g[mid:]))
			groups[i] = slices.Clone(g[:mid])
		}
	}
	var out []PageOut
	for i, g := range groups {
		pg := PageOut{Path: WikiDir + "/" + ps.pageFile(i), ID: ps.idOf(i), Content: ps.content(banner, i, len(groups), g)}
		if len(g) == 1 && slices.Contains(bad, g[0].claimID) {
			pg.Unrenderable = []string{g[0].claimID}
		}
		out = append(out, pg)
	}
	return out
}
