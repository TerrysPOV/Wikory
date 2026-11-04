package bootstrap

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

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
	Origin         = "bootstrap"
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

// PageOut is one rendered page.
type PageOut struct {
	Path, ID string
	Content  []byte
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
	return "component-" + slug + "-" + shortSlugHash(prefix)
}

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

func componentFor(c StoredClaim, inspected []Component) string {
	if prefix, ok := strings.CutPrefix(c.Ref, "comp:"); ok {
		return prefix
	}
	for _, comp := range inspected {
		if comp.Prefix != "." && strings.Contains(c.Text, comp.Prefix) {
			return comp.Prefix
		}
	}
	return ""
}

type placement struct {
	intent, rationale, conflicting, hypotheses, unfinished, general []StoredClaim
	byComponent                                                     map[string][]StoredClaim
}

func place(m RenderModel) placement {
	pl := placement{byComponent: map[string][]StoredClaim{}}
	var inspected []Component
	for _, c := range m.Components {
		if c.Inspected {
			inspected = append(inspected, c)
		}
	}
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
			if comp := componentFor(c, inspected); comp != "" {
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
	read, skipped := 0, 0
	for _, s := range m.Sources {
		if s.Disposition == DispQueued {
			read++
		} else if s.Disposition != DispUnmergedBranch && s.Disposition != DispOtherWorktree {
			skipped++ // a branch or worktree is not a source file
		}
	}
	var inspected, rest []Component
	for _, c := range m.Components {
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
			fmt.Sprintf("This section is a starting point for an existing project. It was built from %d source files that were read (%d more were skipped) and holds %d claims: %d verified, %d conflicting, %d not yet checked and %d historical.",
				read, skipped, len(m.Claims), counts["verified"], counts["conflicting"], counts["unverified"], counts["historical"]),
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
		lines = append(lines, fmt.Sprintf("- `%s`: rank %d, %d files%s, checked against code", c.Prefix, c.Rank, c.Files, entry))
	}
	for _, c := range rest {
		lines = append(lines, fmt.Sprintf("- `%s`: rank %d, %d files, not checked; claims about it stay unverified", c.Prefix, c.Rank, c.Files))
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
		intro := []string{fmt.Sprintf("Component `%s`: rank %d of %d, %d files.", c.Prefix, c.Rank, len(m.Components), c.Files)}
		if !hasRationale {
			intro = append(intro, "No rationale was found for this component in the sources.")
		}
		specs = append(specs, pageSpec{base: componentBase(c.Prefix), typ: "component", title: "Component: " + c.Prefix, intro: intro, secs: secs,
			empty: "No implementation claims were extracted for this component.", links: []string{"bootstrap-overview", "bootstrap-architecture"}})
	}

	var out []PageOut
	for _, sp := range specs {
		out = append(out, sp.pages(banner)...)
	}
	return out
}

type atom struct {
	text    string
	heading bool
	claim   bool
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

func (ps pageSpec) pages(banner string) []PageOut {
	var atoms []atom
	n := 0
	for _, s := range ps.secs {
		if len(s.claims) == 0 {
			continue
		}
		atoms = append(atoms, atom{text: "## " + s.heading + "\n\n", heading: true})
		for _, c := range s.claims {
			atoms = append(atoms, atom{text: c.Meta + "\n" + c.Text + "\n\n", claim: true})
			n++
		}
	}
	if n == 0 && ps.empty != "" {
		atoms = append(atoms, atom{text: ps.empty + "\n\n"})
	}
	// The whole page has to fit MaxPageBytes, not just its claims. What claims may use is what
	// the widest head leaves: the first part, with its introduction, a full link list and a
	// long part number (the slack covers " (part 99999)" in a later title).
	worst := append(append([]string(nil), ps.links...), ps.idOf(99999), ps.idOf(99998), ps.idOf(99997))
	budget := MaxPageBytes - len(ps.head(banner, 0, worst)) - 32
	if budget < minClaimBudget {
		budget = minClaimBudget
	}
	var groups [][]atom
	var cur []atom
	size := 0
	var last atom
	for _, a := range atoms {
		if size+len(a.text) > budget && len(cur) > 0 {
			groups = append(groups, cur)
			cur, size = nil, 0
			if a.claim && last.text != "" {
				cur, size = append(cur, last), len(last.text)
			}
		}
		cur = append(cur, a)
		size += len(a.text)
		if a.heading {
			last = a
		}
	}
	if len(cur) > 0 || len(groups) == 0 {
		groups = append(groups, cur)
	}
	var out []PageOut
	for i, g := range groups {
		var b strings.Builder
		b.WriteString(ps.head(banner, i, ps.linksFor(i, len(groups))))
		for _, a := range g {
			b.WriteString(a.text)
		}
		file := strings.TrimPrefix(ps.idOf(i), "bootstrap-") + ".md"
		out = append(out, PageOut{Path: WikiDir + "/" + file, ID: ps.idOf(i), Content: []byte(b.String())})
	}
	return out
}
