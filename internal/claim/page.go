package claim

import (
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
	if tooNested(lines[start:]) {
		// goldmark's cost grows with the square of the container depth of a line. A page built
		// to stall the indexer is not indexed at all; nothing hidden can leak that way.
		p.Findings = append(p.Findings, Finding{start + 1, "page nests quotes or lists too deeply to index; its body is not indexed"})
		p.ID = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		p.Title = p.ID
		return p
	}
	src := []byte(strings.Join(lines[start:], "\n"))
	w := &pageWalk{src: src, firstLine: start, page: &p, consumed: map[ast.Node]bool{}}
	w.lineStarts = append(w.lineStarts, 0)
	for i, c := range src {
		if c == '\n' {
			w.lineStarts = append(w.lineStarts, i+1)
		}
	}
	if err := w.run(); err != nil {
		p.Findings = append(p.Findings, Finding{start + 1, "page text could not be parsed; its body is not indexed"})
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

// nestingBudget bounds the sum, over the lines of a page, of the square of the length of the
// leading run of quote, list and indentation characters. Real pages use a few thousand; a
// line of 8192 quote marks alone costs the parser about 70 ms, and a megabyte of them 9 s.
const nestingBudget = 20_000_000

func tooNested(lines []string) bool {
	total := 0
	for _, l := range lines {
		m := 0
		for m < len(l) && strings.IndexByte("> \t-*+", l[m]) >= 0 {
			m++
		}
		if total += m * m; total > nestingBudget {
			return true
		}
	}
	return false
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
		switch n := n.(type) {
		case *ast.HTMLBlock:
			w.htmlBlock(n)
			return ast.WalkSkipChildren, nil
		case *ast.Heading:
			t := w.inlineText(n)
			w.heading = strings.TrimSpace(t)
			if w.page.Title == "" && n.Level == 1 {
				w.page.Title = w.heading
			}
			w.add(t)
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph, *ast.TextBlock:
			if !w.consumed[n] {
				w.add(w.inlineText(n))
			}
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			w.add(w.blockLines(n))
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
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
// blank line, a claim comment, a heading or anything else does.
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
			paras = append(paras, w.rawParagraph(n))
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

// rawParagraph returns a paragraph's source lines, right-trimmed, with any raw inline HTML
// (comments inside the paragraph) cut out.
func (w *pageWalk) rawParagraph(p *ast.Paragraph) string {
	var cut []text.Segment
	_ = ast.Walk(p, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if r, ok := n.(*ast.RawHTML); ok && entering {
			for i := 0; i < r.Segments.Len(); i++ {
				cut = append(cut, r.Segments.At(i))
			}
		}
		return ast.WalkContinue, nil
	})
	var out []string
	ls := p.Lines()
	for i := 0; i < ls.Len(); i++ {
		seg := ls.At(i)
		var sb []byte
		for off := seg.Start; off < seg.Stop; off++ {
			inCut := false
			for _, c := range cut {
				if off >= c.Start && off < c.Stop {
					inCut = true
					break
				}
			}
			if !inCut {
				sb = append(sb, w.src[off])
			}
		}
		if line := strings.TrimRight(string(sb), " \t\r\n"); strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// inlineText is the text a renderer shows for a block's inline content: text, code spans and
// the text of emphasis and links. Images, raw HTML, autolinks and every other node are left
// out.
func (w *pageWalk) inlineText(n ast.Node) string {
	var sb strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				sb.Write(visible(c.Segment.Value(w.src)))
				if c.SoftLineBreak() || c.HardLineBreak() {
					sb.WriteByte('\n')
				}
			case *ast.String:
				sb.Write(visible(c.Value))
			case *ast.CodeSpan, *ast.Emphasis, *ast.Link:
				walk(c)
			}
		}
	}
	walk(n)
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
