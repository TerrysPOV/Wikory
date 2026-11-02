package claim

import (
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const samplePage = "---\r\n" +
	"id: queue\r\n" +
	"type: component\r\n" +
	"title: Job queue\r\n" +
	"links: [decision-retries, failure-double-ack]\r\n" +
	"colour: blue\r\n" +
	"---\r\n" +
	"# Job queue\r\n" +
	"Intro prose about the queue.\r\n" +
	"<!-- an editor note -->\r\n" +
	"## Acknowledgement\r\n" +
	"<!-- claim kind=current-impl status=unverified ev=src/queue.go dep=src/queue.go#Ack -->\r\n" +
	"Jobs are acknowledged only after the handler returns,\r\n" +
	"so a crash replays the job.\r\n" +
	"\r\n" +
	"<!-- claim kind=intent status=verified dep=a--b -->\r\n" +
	"Broken metadata.\r\n" +
	"\r\n" +
	"<!-- claim kind=rationale status=unverified -->\r\n" +
	"\r\n" +
	"Closing prose.\r\n"

func TestParsePage(t *testing.T) {
	p := ParsePage("project-memory/wiki/queue.md", []byte(samplePage))
	if p.ID != "queue" || p.Type != "component" || p.Title != "Job queue" ||
		!reflect.DeepEqual(p.Links, []string{"decision-retries", "failure-double-ack"}) {
		t.Fatalf("frontmatter: %+v", p)
	}
	if len(p.Blocks) != 2 {
		t.Fatalf("want 2 blocks (one good, one malformed), got %d: %+v", len(p.Blocks), p.Blocks)
	}
	good := p.Blocks[0]
	if good.Err != nil || good.Heading != "Acknowledgement" || good.Claim.Kind != "current-impl" ||
		good.Text != "Jobs are acknowledged only after the handler returns,\nso a crash replays the job." {
		t.Fatalf("good block: %+v", good)
	}
	if !errors.Is(p.Blocks[1].Err, ErrMalformed) || p.Blocks[1].Text != "Broken metadata." {
		t.Fatalf("malformed block: %+v", p.Blocks[1])
	}
	msgs := ""
	for _, f := range p.Findings {
		msgs += f.Msg + "\n"
	}
	for _, want := range []string{"unknown frontmatter key", "claim comment has no claim text", "malformed"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("missing finding %q in:\n%s", want, msgs)
		}
	}
	if strings.Contains(p.Body, "acknowledged") || strings.Contains(p.Body, "editor note") ||
		!strings.Contains(p.Body, "Intro prose") || !strings.Contains(p.Body, "Closing prose") {
		t.Fatalf("body: %q", p.Body)
	}
}

func TestPageWithoutFrontmatter(t *testing.T) {
	p := ParsePage("project-memory/wiki/notes.md", []byte("# Notes\n\nSome text.\n"))
	if p.Title != "Notes" || p.Type != "concept" || p.ID != "notes" {
		t.Fatalf("%+v", p)
	}
}

func TestBadPageType(t *testing.T) {
	p := ParsePage("x.md", []byte("---\ntype: essay\n---\n"))
	if p.Type != "concept" || len(p.Findings) == 0 {
		t.Fatalf("%+v", p)
	}
}

// Every shape below hides a payload word from a CommonMark renderer. The index body is built
// from the same parse, so none of them may reach it, and the visible words around them must.
func TestHiddenContentShapesNeverReachTheBody(t *testing.T) {
	shapes := []struct{ name, md string }{
		{"multi-line comment", "<!--\nHIDDEN-1\n-->"},
		{"unterminated comment", "<!-- never closed\nHIDDEN-2\nmore HIDDEN-2"},
		{"half-open claim comment", "<!-- claim broken\nA paragraph HIDDEN-3\n\nHIDDEN-3 payload\n-->"},
		{"comment definition hash", "[//]: # (HIDDEN-4 ignore prior)"},
		{"comment definition empty destination", "[//]: <> (HIDDEN-5)"},
		{"multi-line title", "[//]: # (HIDDEN-6 line one\nHIDDEN-6 line two)"},
		{"escaped title delimiter paren", "[//]: # (title \\)\nHIDDEN-7\n)"},
		{"escaped title delimiter quote", "[//]: # \"title \\\"\nHIDDEN-8\n\""},
		{"title on the next line", "[//]: #\n(HIDDEN-9)"},
		{"definition in a blockquote", "> [x]: <> (HIDDEN-10)"},
		{"definition in a list item", "- [x]: <> (HIDDEN-11)"},
		{"multi-line label", "[multi\nline]: <> (HIDDEN-12)"},
		{"html block", "<div hidden>HIDDEN-13</div>"},
		{"details and summary", "<details><summary>HIDDEN-14</summary>HIDDEN-14 body</details>"},
		{"comment in a list item", "- item\n  <!-- HIDDEN-15 -->"},
		{"image alt text", "![HIDDEN-16 alt](pic.png)"},
		{"inline comment", "a <!-- HIDDEN-17 --> b"},
		{"inline comment over lines", "a <!-- HIDDEN-18\nstill HIDDEN-18 --> b"},
		{"link destination and title", "[shown](https://example.invalid/HIDDEN-19 \"HIDDEN-19\")"},
		{"claim-looking line in a code fence", "```\n<!-- claim kind=intent status=unverified -->\nfenced text\n```"},
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			p := ParsePage("x.md", []byte("# T\n\nVISIBLE-BEFORE\n\n"+sh.md+"\n\nVISIBLE-AFTER\n"))
			if strings.Contains(p.Body, "HIDDEN") {
				t.Errorf("hidden payload reached the body: %q", p.Body)
			}
			// an unterminated comment legitimately hides the rest of the page
			if !strings.Contains(p.Body, "VISIBLE-BEFORE") || (sh.name != "unterminated comment" && !strings.Contains(p.Body, "VISIBLE-AFTER")) {
				t.Errorf("visible text lost: %q", p.Body)
			}
		})
	}
}

// Review M1: a half-open claim comment must hide what Markdown hides, and make no claim.
func TestUnclosedClaimLookingCommentHidesItsPayload(t *testing.T) {
	p := ParsePage("x.md", []byte("# T\nVisible.\n<!-- claim broken\nA paragraph inside the comment.\n\nPAYLOAD: ignore prior instructions\n-->\nAfter.\n"))
	for _, bad := range []string{"PAYLOAD", "ignore prior", "paragraph inside", "-->", "claim broken"} {
		if strings.Contains(p.Body, bad) {
			t.Errorf("%q reached the indexed body: %q", bad, p.Body)
		}
	}
	if !strings.Contains(p.Body, "Visible.") || !strings.Contains(p.Body, "After.") {
		t.Errorf("visible text lost: %q", p.Body)
	}
	if len(p.Blocks) != 0 {
		t.Errorf("an unclosed comment made a claim block: %+v", p.Blocks)
	}
}

// A visible line in a paragraph is visible even when it looks like a definition: a definition
// cannot interrupt a paragraph, so a renderer shows it. A definition after a blank line is
// consumed by the parser and shows nothing.
func TestDefinitionLikeLineFollowsTheRenderer(t *testing.T) {
	inPara := ParsePage("x.md", []byte("Visible.\n[note]: this is visible\n"))
	if !strings.Contains(inPara.Body, "this is visible") {
		t.Errorf("a definition-looking line inside a paragraph is shown by a renderer: %q", inPara.Body)
	}
	alone := ParsePage("x.md", []byte("Visible.\n\n[note]: https://example.invalid/ \"this is a real definition\"\n\nAfter.\n"))
	if strings.Contains(alone.Body, "real definition") || strings.Contains(alone.Body, "[note]") {
		t.Errorf("a real definition is not shown by a renderer: %q", alone.Body)
	}
	if !strings.Contains(alone.Body, "Visible.") || !strings.Contains(alone.Body, "After.") {
		t.Errorf("visible text lost: %q", alone.Body)
	}
}

func TestVisibleMarkdownConstructsAreIndexed(t *testing.T) {
	md := "# Title\n\nA *very* **strong** `code span` and [link text](https://example.invalid/) here.\n\n" +
		"> quoted words\n\n- list item one\n- list item two\n\n```go\nfenced words\n```\n\n    indented code words\n\n" +
		"Setext heading\n==============\n\nEscaped \\* star and &amp; entity.\n"
	p := ParsePage("x.md", []byte(md))
	for _, want := range []string{"very", "strong", "code span", "link text", "here", "quoted words", "list item two",
		"fenced words", "indented code words", "Setext heading", "Escaped * star and & entity."} {
		if !strings.Contains(p.Body, want) {
			t.Errorf("%q missing from body: %q", want, p.Body)
		}
	}
	for _, bad := range []string{"https://example.invalid", "```", "[link", "**"} {
		if strings.Contains(p.Body, bad) {
			t.Errorf("%q should not be in body: %q", bad, p.Body)
		}
	}
	if p.Title != "Title" {
		t.Errorf("title %q", p.Title)
	}
}

func TestClaimsAreOnlyRecognisedWhereARendererShowsAnHTMLComment(t *testing.T) {
	meta := "<!-- claim kind=intent status=unverified -->"
	good := meta + "\nReal claim.\n"
	for name, md := range map[string]string{
		"in a code fence":            "```\n" + good + "```\n",
		"in an indented code":        "    " + meta + "\n    Real claim.\n",
		"in a blockquote":            "> " + meta + "\n> Real claim.\n",
		"in a list item":             "- " + meta + "\n  Real claim.\n",
		"inside a longer HTML block": "<div>\n" + good + "</div>\n",
	} {
		p := ParsePage("x.md", []byte(md))
		if len(p.Blocks) != 0 {
			t.Errorf("%s: made a claim: %+v", name, p.Blocks)
		}
	}
	p := ParsePage("x.md", []byte(good))
	if len(p.Blocks) != 1 || p.Blocks[0].Text != "Real claim." || p.Blocks[0].Line != 1 {
		t.Errorf("a real claim was not found: %+v", p.Blocks)
	}
}

func TestClaimLookingLinesThatAreNotCompleteMakeNoBlockAndNoClaimText(t *testing.T) {
	for name, line := range map[string]string{
		"nested opener":       "<!-- claim kind=intent <!-- nested -->",
		"text after marker":   "<!-- claim kind=intent status=unverified --> visible tail",
		"opener after marker": "<!-- claim kind=intent status=unverified --> <!--",
	} {
		p := ParsePage("x.md", []byte(line+"\nPlain paragraph PAYLOAD.\n"))
		if len(p.Blocks) != 0 {
			t.Errorf("%s: made a claim block: %+v", name, p.Blocks)
		}
		found := false
		for _, f := range p.Findings {
			found = found || strings.Contains(f.Msg, "not a complete one-line comment")
		}
		if !found {
			t.Errorf("%s: no finding: %+v", name, p.Findings)
		}
	}
}

// Review M1: text between inline HTML tags can be hidden by a browser (`<span hidden>`), so a
// paragraph or heading holding raw inline HTML is left out of the body, and said so.
func TestParagraphsAndHeadingsWithInlineHTMLAreOmitted(t *testing.T) {
	for name, md := range map[string]string{
		"paragraph":        "Para <span hidden>HIDDEN-X</span> words.",
		"atx heading":      "# Head <span hidden>HIDDEN-X</span>",
		"setext heading":   "Head <span hidden>HIDDEN-X</span>\n=====",
		"inline comment":   "Para <!-- HIDDEN-X --> words.",
		"list item":        "- item <b hidden>HIDDEN-X</b>",
		"quoted paragraph": "> quote <i hidden>HIDDEN-X</i>",
	} {
		p := ParsePage("x.md", []byte("# Real title\n\nVISIBLE-PARA\n\n"+md+"\n\nVISIBLE-AFTER\n"))
		if strings.Contains(p.Body, "HIDDEN") || strings.Contains(p.Title, "HIDDEN") {
			t.Errorf("%s: hidden text reached the index: title %q body %q", name, p.Title, p.Body)
		}
		if !strings.Contains(p.Body, "VISIBLE-PARA") || !strings.Contains(p.Body, "VISIBLE-AFTER") {
			t.Errorf("%s: visible text lost: %q", name, p.Body)
		}
		found := false
		for _, f := range p.Findings {
			found = found || strings.Contains(f.Msg, "inline HTML")
		}
		if !found {
			t.Errorf("%s: the omission was silent: %+v", name, p.Findings)
		}
	}
	p := ParsePage("x.md", []byte("# T <span hidden>HIDDEN-X</span>\n\ntext\n"))
	if p.Title == "" || strings.Contains(p.Title, "HIDDEN") || !strings.Contains(p.Body, "[heading omitted]") {
		t.Errorf("title %q body %q", p.Title, p.Body)
	}
	q := ParsePage("x.md", []byte("# H <span hidden>HIDDEN-X</span>\n\n<!-- claim kind=intent status=unverified -->\nClaim.\n"))
	if len(q.Blocks) != 1 || q.Blocks[0].Heading != "[heading omitted]" {
		t.Errorf("claim heading: %+v", q.Blocks)
	}
}

// Review L4: a code span is displayed literally, so entities and escapes in it are not decoded.
func TestCodeSpanTextIsIndexedLiterally(t *testing.T) {
	p := ParsePage("x.md", []byte("Use `&lt;x&gt; and \\* here` and &lt;y&gt; outside.\n"))
	if !strings.Contains(p.Body, "&lt;x&gt; and \\* here") || !strings.Contains(p.Body, "<y> outside") {
		t.Errorf("body %q", p.Body)
	}
}

// Review H1: goldmark's time is quadratic in the number of link and emphasis delimiters. The
// bounds are checked before the parser runs. Every input below EXCEEDS its bound.
func TestInlineStructureBeyondTheBoundsIsRefusedWithoutParsing(t *testing.T) {
	for name, md := range map[string]string{
		"links 240 KB":       strings.Repeat(strings.Repeat("[a](", 20)+"\n", 3000),
		"emphasis 255 KB":    strings.Repeat(strings.Repeat("*a_", 28)+"\n", 3040),
		"brackets 1501":      strings.Repeat("[", 1501),
		"close-open 1501":    strings.Repeat("a](", 1501),
		"delimiters 4001":    strings.Repeat("*", 4001),
		"underscores 4001":   strings.Repeat("_", 4001),
		"mixed delims 4001":  strings.Repeat("*_", 2000) + "*",
		"backticks 2001":     strings.Repeat("`", 2001),
		"links in a heading": "# " + strings.Repeat("[a](", 1501),
	} {
		start := time.Now()
		p := ParsePage("x.md", []byte("# Title\n\n"+md+"\n"))
		found := false
		for _, f := range p.Findings {
			found = found || strings.Contains(f.Msg, "inline structure too complex")
		}
		if !found || p.Body != "" || p.Title == "" {
			t.Errorf("%s: finding=%v body length %d title %q", name, found, len(p.Body), p.Title)
		}
		if el := time.Since(start); el > time.Second*slack() {
			t.Errorf("%s: took %v", name, el)
		}
	}
}

// The review's two shapes as one unbroken line: the page is over the size cap, the line is cut
// out of the parsed prefix, and nothing slow runs.
func TestOneHugeLineOfDelimitersIsFast(t *testing.T) {
	for name, line := range map[string]string{
		"links":    strings.Repeat("[a](", 60000),
		"emphasis": strings.Repeat("*a_", 85000),
	} {
		start := time.Now()
		p := ParsePage("x.md", []byte("# Title\n\n"+line+"\n"))
		if el := time.Since(start); el > time.Second*slack() || p.Title != "Title" {
			t.Errorf("%s: took %v, title %q", name, el, p.Title)
		}
	}
}

// The worst page the bounds still allow parses quickly: every bound at its limit at once.
func TestWorstAllowedPageParsesWithinTheBudget(t *testing.T) {
	md := "# Title\n\n" + strings.Repeat("[a](", 750) + strings.Repeat("![a](", 750) +
		strings.Repeat("*a_", 2000) + strings.Repeat("`a", 2000) + "\n"
	// Best of three: one slow run on a loaded machine is not a slow parser, but a parser that
	// is slow every time still fails.
	var p Page
	el := time.Duration(1 << 62)
	for i := 0; i < 3; i++ {
		start := time.Now()
		p = ParsePage("x.md", []byte(md))
		if d := time.Since(start); d < el {
			el = d
		}
	}
	for _, f := range p.Findings {
		if strings.Contains(f.Msg, "too complex") {
			t.Fatalf("the allowed page was refused: %+v (size %d)", f, len(md))
		}
	}
	// The race detector slows the parser several-fold, so the budget is measured without it.
	if limit := 250 * time.Millisecond * slack(); el > limit && !raceEnabled {
		t.Errorf("worst allowed page took %v, limit %v", el, limit)
	}
	if p.Body == "" {
		t.Errorf("an allowed page was not indexed")
	}
}

func TestClaimCommentsSurviveCommentRemoval(t *testing.T) {
	page := "# T\n<!--\nhidden\n-->\n" +
		"<!-- claim kind=intent status=unverified -->\n" +
		"First claim line.\n" +
		"<!-- inline note -->\n" +
		"<!--\nmulti\nline note\n-->\n" +
		"Second claim line.\n\n" +
		"<!-- claim kind=rationale status=unverified -->\nAnother claim.\n"
	p := ParsePage("x.md", []byte(page))
	if len(p.Blocks) != 2 || p.Blocks[0].Err != nil || p.Blocks[1].Err != nil {
		t.Fatalf("blocks: %+v", p.Blocks)
	}
	if got := p.Blocks[0].Text; got != "First claim line.\nSecond claim line." {
		t.Errorf("a comment inside a claim paragraph leaked into it, or split it: %q", got)
	}
	if p.Blocks[0].Line != 5 || p.Blocks[1].Line != 14 {
		t.Errorf("source line numbers moved: %d, %d", p.Blocks[0].Line, p.Blocks[1].Line)
	}
	if strings.Contains(p.Body, "hidden") || strings.Contains(p.Body, "claim") {
		t.Errorf("body: %q", p.Body)
	}
}

// Review: the displayed and indexed claim text is the visible text of the paragraph. A link
// title, image alt text, an autolink destination or inline HTML must not be carried in it.
func TestClaimTextIsVisibleTextOnly(t *testing.T) {
	meta := "<!-- claim kind=intent status=unverified -->\n"
	p := ParsePage("x.md", []byte(meta+"Use the [queue](https://example.invalid/q) and `Ack()` with *care*.\n"))
	if len(p.Blocks) != 1 || p.Blocks[0].Text != "Use the queue and Ack() with care." {
		t.Fatalf("visible text of a plain claim paragraph: %+v", p.Blocks)
	}
	for name, para := range map[string]string{
		"titled link":     "Read [the docs](https://example.invalid/ \"HIDDEN-TITLE\") first.",
		"image alt text":  "See ![HIDDEN-ALT](pic.png) here.",
		"autolink":        "Go to <https://example.invalid/HIDDEN-AUTO> now.",
		"inline comment":  "Before <!-- HIDDEN-C --> after.",
		"inline html tag": "A <span hidden>HIDDEN-SPAN</span> b.",
	} {
		q := ParsePage("x.md", []byte(meta+para+"\n"))
		for _, b := range q.Blocks {
			if strings.Contains(b.Text, "HIDDEN") {
				t.Errorf("%s: hidden text reached the claim text: %q", name, b.Text)
			}
		}
		if strings.Contains(q.Body, "HIDDEN") {
			t.Errorf("%s: hidden text reached the body: %q", name, q.Body)
		}
		found := false
		for _, f := range q.Findings {
			found = found || strings.Contains(f.Msg, "excluded from the claim")
		}
		if !found {
			t.Errorf("%s: the exclusion was silent: %+v", name, q.Findings)
		}
	}
}

// A paragraph with a hundred thousand inline comments must not cost quadratic time.
func TestManyInlineCutsInAClaimParagraphAreBounded(t *testing.T) {
	start := time.Now()
	p := ParsePage("x.md", []byte("<!-- claim kind=intent status=unverified -->\n"+strings.Repeat("a<!--x-->", 20000)+"\n"))
	limit := 5 * time.Second * slack()
	if el := time.Since(start); el > limit {
		t.Errorf("parsing took %v, limit %v", el, limit)
	}
	if len(p.Blocks) != 0 {
		t.Errorf("a paragraph full of inline HTML made a claim: %d blocks", len(p.Blocks))
	}
}

func slack() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("WIKORY_LATENCY_SLACK")); err == nil && n > 0 {
		return time.Duration(n)
	}
	return 1
}

// Line numbers stay file lines when a frontmatter block precedes the claim (stamp rewrites by line).
func TestClaimLineNumbersCountFrontmatter(t *testing.T) {
	p := ParsePage("x.md", []byte("---\nid: a\n---\n\ntext\n\n<!-- claim kind=intent status=unverified -->\nClaim.\n"))
	if len(p.Blocks) != 1 || p.Blocks[0].Line != 7 {
		t.Fatalf("blocks: %+v", p.Blocks)
	}
}

// A page built to stall the parser (the cost grows with the square of the container depth of
// a line) is refused with a finding rather than parsed. Quote marks, bullet markers, ordered
// list markers and indentation all open containers.
func TestPathologicalNestingIsRefusedNotParsed(t *testing.T) {
	for name, md := range map[string]string{
		"quotes":          strings.Repeat(strings.Repeat(">", 8192)+"x\n", 20),
		"bullets":         strings.Repeat(strings.Repeat("- ", 4096)+"x\n", 20),
		"ordered markers": strings.Repeat(strings.Repeat("1. ", 10000)+"x\n", 8),
		"ordered parens":  strings.Repeat(strings.Repeat("1) ", 10000)+"x\n", 8),
		"mixed":           strings.Repeat(strings.Repeat("> 1. - ", 3000)+"x\n", 10),
		"indentation":     strings.Repeat(strings.Repeat(" ", 4000)+"- x\n", 20),
	} {
		start := time.Now()
		p := ParsePage("x.md", []byte("# T\n\n"+md))
		found := false
		for _, f := range p.Findings {
			found = found || strings.Contains(f.Msg, "nesting too deep")
		}
		if !found || p.Body != "" || p.Title == "" {
			t.Errorf("%s: finding=%v body length %d title %q", name, found, len(p.Body), p.Title)
		}
		if el := time.Since(start); el > 5*time.Second*slack() {
			t.Errorf("%s: took %v", name, el)
		}
	}
	// ordinary nesting is untouched
	p := ParsePage("x.md", []byte("> a\n> > b\n> > > c\n\n- one\n  - two\n    - three\n\n1. first\n   1. inner\n"))
	if !strings.Contains(p.Body, "c") || !strings.Contains(p.Body, "three") || !strings.Contains(p.Body, "inner") || p.Findings != nil {
		t.Errorf("ordinary nesting refused: %+v", p)
	}
}

// Over 64 KiB the parser is not run over the whole page: its title and headings are indexed
// from the first 64 KiB, and the page says so.
func TestOversizePageIsIndexedAsTitleAndHeadingsOnly(t *testing.T) {
	big := "# Big page\n\n## Early heading\n\nEARLY-PARAGRAPH\n\n<!--\n## HIDDEN-HEADING\n-->\n\n" +
		strings.Repeat("filler line of ordinary text\n\n", 3000) + "## Late heading\n"
	p := ParsePage("x.md", []byte(big))
	if len(big) <= 64<<10 {
		t.Fatalf("fixture too small: %d", len(big))
	}
	found := false
	for _, f := range p.Findings {
		found = found || strings.Contains(f.Msg, "larger than 64 KiB")
	}
	if !found {
		t.Errorf("no finding: %+v", p.Findings)
	}
	if p.Title != "Big page" || !strings.Contains(p.Body, "Early heading") {
		t.Errorf("title %q body %q", p.Title, p.Body)
	}
	for _, bad := range []string{"EARLY-PARAGRAPH", "filler", "HIDDEN-HEADING"} {
		if strings.Contains(p.Body, bad) {
			t.Errorf("%q in a headings-only body", bad)
		}
	}
}
