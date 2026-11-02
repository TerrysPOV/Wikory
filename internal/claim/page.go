package claim

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// PageTypes are the page types of spec 5.4.
var PageTypes = map[string]bool{"component": true, "decision": true, "lesson": true,
	"failure-signature": true, "runbook": true, "concept": true, "file-companion": true}

// Finding is a non-fatal problem found while parsing a page.
type Finding struct {
	Line int
	Msg  string
}

// Block is one claim: its metadata comment and the paragraph that follows it.
type Block struct {
	Claim    Claim
	Text     string
	Heading  string
	MetaLine string
	Line     int
	Err      error
}

// Page is a parsed wiki page.
type Page struct {
	RelPath  string
	ID       string
	Type     string
	Title    string
	Links    []string
	Blocks   []Block
	Body     string
	Findings []Finding
	// Degraded is the first reason part of the page was left out of the index (size, structure,
	// inline HTML), or "". The indexer stores it so a refresh can disclose it without parsing.
	Degraded string
}

// degrade records the first reason the page is indexed in part.
func (p *Page) degrade(why string) {
	if p.Degraded == "" {
		p.Degraded = why
	}
}

// ParsePage parses a page. It never fails: problems become findings.
//
// The body that reaches the index is built from a CommonMark parse (goldmark, no extensions)
// of the text after the frontmatter, from the text of visible nodes only: text, code spans and
// code blocks, headings, paragraphs, list items, quoted text, and the text of emphasis and
// links. HTML blocks, raw inline HTML, comments, image alt text and link reference
// definitions contribute nothing, so what the index holds is what a renderer shows and never
// something it hides. Claim comments are recognised on top of that parse: a claim comment is
// a one-line HTML block at the top level of the page, so a claim-looking line inside a code
// fence, a quote, a list item or a longer HTML block is not a claim.
func ParsePage(rel string, data []byte) Page {
	lines := strings.Split(normaliseNewlines(string(data)), "\n")
	p := Page{RelPath: rel, Type: "concept"}
	start := p.parseFrontmatter(lines)
	src := []byte(strings.Join(lines[start:], "\n"))
	headingsOnly := false
	if len(src) > maxParseBytes {
		// Parse a bounded prefix, cut at a line end, and keep only the title and headings.
		cut := bytes.LastIndexByte(src[:maxParseBytes], '\n')
		if cut < 0 {
			cut = 0
		}
		src, headingsOnly = src[:cut], true
		p.Findings = append(p.Findings, Finding{start + 1, "page is larger than 64 KiB; only its title and the headings in its first 64 KiB are indexed"})
		p.degrade("larger than 64 KiB: only its title and headings are indexed")
	}
	if tooNested(strings.Split(string(src), "\n")) {
		// The parser's cost grows with the square of a line's container depth. A page built to
		// stall the indexer is indexed by its title alone; nothing hidden can leak that way.
		return p.titleOnly(rel, start, "page nesting too deep; only its title is indexed")
	}
	if tooComplex(src) {
		return p.titleOnly(rel, start, "inline structure too complex; only its title is indexed")
	}
	w := &pageWalk{src: src, firstLine: start, page: &p, consumed: map[ast.Node]bool{}, headingsOnly: headingsOnly}
	w.lineStarts = append(w.lineStarts, 0)
	for i, c := range src {
		if c == '\n' {
			w.lineStarts = append(w.lineStarts, i+1)
		}
	}
	if err := w.run(); err != nil {
		p.Findings = append(p.Findings, Finding{start + 1, "page text could not be parsed; its body is not indexed"})
		p.degrade("the page text could not be parsed")
		p.Blocks, w.body = nil, nil
	}
	p.Body = strings.TrimSpace(strings.Join(w.body, "\n"))
	if p.ID == "" {
		p.ID = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	}
	if p.Title == "" {
		p.Title = p.ID
	}
	return p
}

// titleOnly finishes a page that is indexed by its title alone, with the reason as a finding.
func (p Page) titleOnly(rel string, start int, why string) Page {
	p.Findings = append(p.Findings, Finding{start + 1, why})
	p.degrade(why)
	p.Blocks, p.Body = nil, ""
	if p.ID == "" {
		p.ID = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	}
	if p.Title == "" {
		p.Title = p.ID
	}
	return p
}

// ParsePageTitleOnly reads only the frontmatter of a page and indexes the rest as nothing,
// with why recorded as a finding. The indexer uses it for pages it has no parse time left for.
func ParsePageTitleOnly(rel string, data []byte, why string) Page {
	lines := strings.Split(normaliseNewlines(string(data)), "\n")
	p := Page{RelPath: rel, Type: "concept"}
	start := p.parseFrontmatter(lines)
	return p.titleOnly(rel, start, why)
}

// Bounds on the inline structure the parser is given. goldmark's time grows with the square of
// the number of link and emphasis delimiters, so these are checked before it runs. Measured at
// the limits all together, a page parses in well under 250 ms; the review's inputs (`[a](`
// repeated, `*a_` repeated) took 7 s at 80 KB and over a minute at 240 KB.
const (
	maxBrackets  = 1500 // '[' characters, and separately "](" pairs
	maxEmphasis  = 4000 // '*' and '_' characters together
	maxBackticks = 2000
)

// tooComplex reports whether src holds more link, emphasis or code-span delimiters than the
// bounds allow. It counts every byte, code blocks included: it is a cost bound, not a parse.
func tooComplex(src []byte) bool {
	brackets, closeOpen, emphasis, ticks := 0, 0, 0, 0
	for i, c := range src {
		switch c {
		case '[':
			brackets++
		case ']':
			if i+1 < len(src) && src[i+1] == '(' {
				closeOpen++
			}
		case '*', '_':
			emphasis++
		case '`':
			ticks++
		}
	}
	return brackets > maxBrackets || closeOpen > maxBrackets || emphasis > maxEmphasis || ticks > maxBackticks
}

const (
	// maxParseBytes is the most page text the parser sees. A larger page is indexed by its
	// title and headings from the first maxParseBytes only.
	maxParseBytes = 64 << 10
	// maxNesting is the deepest container nesting a line may open: quote marks, bullet and
	// ordered list markers, and indentation (four columns count as one level). The parser's cost
	// grows with the square of this depth on every line, so a page beyond it is not parsed.
	maxNesting = 32
)

func tooNested(lines []string) bool {
	for _, l := range lines {
		if lineDepth(l) > maxNesting {
			return true
		}
	}
	return false
}

// lineDepth counts the container openers at the start of a line, stopping as soon as the
// count passes maxNesting so the scan itself stays short.
func lineDepth(l string) int {
	i, col := 0, 0
	for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
		if l[i] == '\t' {
			col += 4
		} else {
			col++
		}
		i++
	}
	d := col / 4
	for i < len(l) && d <= maxNesting {
		c := l[i]
		switch {
		case c == '>':
			i++
		case (c == '-' || c == '*' || c == '+') && (i+1 == len(l) || l[i+1] == ' ' || l[i+1] == '\t'):
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(l) && j-i < 10 && l[j] >= '0' && l[j] <= '9' {
				j++
			}
			if j < len(l) && (l[j] == '.' || l[j] == ')') && (j+1 == len(l) || l[j+1] == ' ' || l[j+1] == '\t') {
				i = j + 1
			} else {
				return d
			}
		default:
			return d
		}
		d++
		for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
			i++
		}
	}
	return d
}

// pageWalk carries the state of one walk over a page's CommonMark tree.
type pageWalk struct {
	src        []byte
	firstLine  int   // index in the file of the first line of src
	lineStarts []int // byte offset of each line of src
	page       *Page
	body       []string
	heading    string
	consumed   map[ast.Node]bool // paragraphs already taken as claim text
	// headingsOnly keeps only headings: the page was too large to index in full.
	headingsOnly bool
}

func (w *pageWalk) run() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("markdown parser: %v", r)
		}
	}()
	doc := goldmark.New().Parser().Parse(text.NewReader(w.src))
	return ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if w.headingsOnly {
			if h, ok := n.(*ast.Heading); ok {
				w.heading_(h)
			}
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.HTMLBlock:
			w.htmlBlock(n)
			return ast.WalkSkipChildren, nil
		case *ast.Heading:
			w.heading_(n)
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph, *ast.TextBlock:
			if w.consumed[n] {
				return ast.WalkSkipChildren, nil
			}
			if hasRawHTML(n) {
				first, _ := w.span(n)
				w.page.Findings = append(w.page.Findings, Finding{w.firstLine + first + 1, "a paragraph with inline HTML is omitted from the index"})
				w.page.degrade("a paragraph with inline HTML was omitted")
				return ast.WalkSkipChildren, nil
			}
			w.add(w.inlineText(n))
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			w.add(w.blockLines(n))
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
}

// heading_ records a heading as the current heading, the title when it is the first top-level
// one, and body text. A heading holding raw inline HTML is recorded as "[heading omitted]",
// with a finding, and never becomes the title.
func (w *pageWalk) heading_(h *ast.Heading) {
	if hasRawHTML(h) {
		first, _ := w.span(h)
		w.page.Findings = append(w.page.Findings, Finding{w.firstLine + first + 1, "a heading with inline HTML is omitted from the index"})
		w.page.degrade("a heading with inline HTML was omitted")
		w.heading = "[heading omitted]"
		w.add(w.heading)
		return
	}
	t := w.inlineText(h)
	w.heading = strings.TrimSpace(t)
	if w.page.Title == "" && h.Level == 1 {
		w.page.Title = w.heading
	}
	w.add(t)
}

// hasRawHTML reports whether a block's inline content holds raw inline HTML, whose tags can
// hide the text between them from a reader while CommonMark shows it.
func hasRawHTML(n ast.Node) bool {
	found := false
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if _, ok := c.(*ast.RawHTML); ok && entering {
			found = true
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

func (w *pageWalk) add(s string) {
	if strings.TrimSpace(s) != "" {
		w.body = append(w.body, s)
	}
}

// lineOf is the index, within src, of the line holding byte offset off.
func (w *pageWalk) lineOf(off int) int {
	return sort.Search(len(w.lineStarts), func(i int) bool { return w.lineStarts[i] > off }) - 1
}

// span returns the first and last line (within src) a block occupies.
func (w *pageWalk) span(n ast.Node) (first, last int) {
	first, last = -1, -1
	see := func(seg text.Segment) {
		if seg.Start < 0 {
			return
		}
		a, z := w.lineOf(seg.Start), w.lineOf(max(seg.Start, seg.Stop-1))
		if first < 0 || a < first {
			first = a
		}
		if z > last {
			last = z
		}
	}
	ls := n.Lines()
	for i := 0; i < ls.Len(); i++ {
		see(ls.At(i))
	}
	if h, ok := n.(*ast.HTMLBlock); ok && h.HasClosure() {
		see(h.ClosureLine)
	}
	return first, last
}

// htmlBlock handles an HTML block: it contributes no body text. A one-line block at the top
// level of the page that reads as a claim comment starts a claim.
func (w *pageWalk) htmlBlock(n *ast.HTMLBlock) {
	first, last := w.span(n)
	if first < 0 {
		return
	}
	meta := strings.TrimSpace(string(n.Text(w.src)))
	if !IsClaimComment(meta) {
		return
	}
	line := w.firstLine + first + 1
	if first != last || !isCompleteClaimComment(meta) || n.Parent() == nil || n.Parent().Kind() != ast.KindDocument {
		w.page.Findings = append(w.page.Findings, Finding{line, "claim comment is not a complete one-line comment at the top level; ignored"})
		return
	}
	b := Block{MetaLine: meta, Line: line, Heading: w.heading}
	b.Claim, b.Err = ParseComment(meta)
	b.Text = w.claimText(n, last)
	if b.Text == "" {
		w.page.Findings = append(w.page.Findings, Finding{line, "claim comment has no claim text; skipped"})
		return
	}
	if b.Err != nil {
		w.page.Findings = append(w.page.Findings, Finding{b.Line, b.Err.Error()})
	}
	w.page.Blocks = append(w.page.Blocks, b)
}

// isCompleteClaimComment reports whether a line is one whole claim comment: the claim prefix,
// one closing marker as the last thing on the line, and no other comment marker.
func isCompleteClaimComment(line string) bool {
	s := strings.TrimSpace(line)
	return IsClaimComment(s) && strings.HasSuffix(s, "-->") &&
		strings.Count(s, "<!--") == 1 && strings.Count(s, "-->") == 1
}

// claimText collects the paragraphs that follow a claim comment with no blank line between
// them. Other HTML blocks in between (editor notes) are skipped and do not end the claim; a
// blank line, a claim comment, a heading or anything else does. The text is the visible text
// of each paragraph, the same extraction the body uses. A paragraph holding anything whose
// source differs from what a renderer shows (an image, a titled link, an autolink, raw inline
// HTML) is left out of the claim and reported, never silently.
func (w *pageWalk) claimText(claimNode ast.Node, prevEnd int) string {
	var paras []string
	for n := claimNode.NextSibling(); n != nil; n = n.NextSibling() {
		first, last := w.span(n)
		if first != prevEnd+1 {
			break
		}
		switch n := n.(type) {
		case *ast.Paragraph:
			w.consumed[n] = true
			if what := hiddenCarrier(n); what != "" {
				w.page.Findings = append(w.page.Findings, Finding{w.firstLine + first + 1,
					"a paragraph with " + what + " is excluded from the claim"})
				w.page.degrade("a claim paragraph with " + what + " was excluded")
			} else if t := strings.TrimRight(w.inlineText(n), " \t\n"); t != "" {
				paras = append(paras, t)
			}
		case *ast.HTMLBlock:
			if m := strings.TrimSpace(string(n.Text(w.src))); isCompleteClaimComment(m) && first == last {
				return strings.Join(paras, "\n")
			}
		default:
			return strings.Join(paras, "\n")
		}
		prevEnd = last
	}
	return strings.Join(paras, "\n")
}

// hiddenCarrier names the first inline node in a paragraph that can carry text the visible
// text omits, or returns "".
func hiddenCarrier(p *ast.Paragraph) string {
	what := ""
	_ = ast.Walk(p, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Image:
			what = "an image"
		case *ast.AutoLink:
			what = "an autolink"
		case *ast.RawHTML:
			what = "inline HTML"
		case *ast.Link:
			if len(n.Title) > 0 {
				what = "a titled link"
			}
		}
		if what != "" {
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return what
}

// inlineText is the text a renderer shows for a block's inline content: text, code spans and
// the text of emphasis and links. Images, raw HTML, autolinks and every other node are left
// out.
func (w *pageWalk) inlineText(n ast.Node) string {
	var sb strings.Builder
	var walk func(n ast.Node, literal bool)
	walk = func(n ast.Node, literal bool) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				v := c.Segment.Value(w.src)
				if !literal {
					v = visible(v)
				}
				sb.Write(v)
				if c.SoftLineBreak() || c.HardLineBreak() {
					sb.WriteByte('\n')
				}
			case *ast.String:
				sb.Write(visible(c.Value))
			case *ast.CodeSpan:
				walk(c, true) // a code span is shown literally: no entities, no escapes
			case *ast.Emphasis, *ast.Link:
				walk(c, literal)
			}
		}
	}
	walk(n, false)
	return sb.String()
}

// blockLines is the text of a code block's lines (the info string is not shown).
func (w *pageWalk) blockLines(n ast.Node) string {
	var sb strings.Builder
	ls := n.Lines()
	for i := 0; i < ls.Len(); i++ {
		seg := ls.At(i)
		sb.Write(seg.Value(w.src))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// visible resolves backslash escapes and character references the way a renderer does.
func visible(b []byte) []byte {
	return util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(b)))
}

// parseFrontmatter fills page fields and returns the first body line index.
func (p *Page) parseFrontmatter(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	end := -1
	for i := 1; i < len(lines) && i < 200; i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		p.Findings = append(p.Findings, Finding{1, "unterminated frontmatter; treated as body"})
		return 0
	}
	for i := 1; i < end; i++ {
		k, v, ok := strings.Cut(lines[i], ":")
		if !ok {
			p.Findings = append(p.Findings, Finding{i + 1, "frontmatter line is not key: value"})
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "id":
			p.ID = v
		case "title":
			p.Title = v
		case "scope":
			if v != "repo" {
				p.Findings = append(p.Findings, Finding{i + 1, "page scope must be repo"})
			}
		case "type":
			if PageTypes[v] {
				p.Type = v
			} else {
				p.Findings = append(p.Findings, Finding{i + 1, fmt.Sprintf("unknown page type %q; using concept", v)})
			}
		case "links":
			inner := strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
			for _, l := range strings.Split(inner, ",") {
				if l = strings.TrimSpace(l); l != "" {
					p.Links = append(p.Links, l)
				}
			}
		default:
			p.Findings = append(p.Findings, Finding{i + 1, fmt.Sprintf("unknown frontmatter key %q", k)})
		}
	}
	return end + 1
}
