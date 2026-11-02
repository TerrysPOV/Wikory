package claim

import (
	"fmt"
	"path"
	"regexp"
	"strings"
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
func ParsePage(rel string, data []byte) Page {
	lines := strings.Split(normaliseNewlines(string(data)), "\n")
	p := Page{RelPath: rel, Type: "concept"}
	start := p.parseFrontmatter(lines)
	var body []string
	heading := ""
	clean, dropped, defs := stripComments(lines, start)
	for _, d := range defs {
		p.Findings = append(p.Findings, Finding{d.line, fmt.Sprintf("link-definition-like text ignored (%d lines)", d.n)})
	}
	for i := start; i < len(lines); i++ {
		if dropped[i] {
			continue
		}
		line := clean[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case isHeading(trimmed):
			heading = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if p.Title == "" && strings.HasPrefix(trimmed, "# ") {
				p.Title = heading
			}
			body = append(body, line)
		case IsClaimComment(trimmed):
			b := Block{MetaLine: trimmed, Line: i + 1, Heading: heading}
			b.Claim, b.Err = ParseComment(trimmed)
			var para []string
			for i+1 < len(lines) {
				if dropped[i+1] { // a comment inside the paragraph is removed; it does not end it
					i++
					continue
				}
				next := strings.TrimSpace(clean[i+1])
				if next == "" || IsClaimComment(next) || isHeading(next) {
					break
				}
				para = append(para, strings.TrimRight(clean[i+1], " \t"))
				i++
			}
			b.Text = strings.Join(para, "\n")
			if b.Text == "" {
				p.Findings = append(p.Findings, Finding{i + 1, "claim comment has no claim text; skipped"})
				continue
			}
			if b.Err != nil {
				p.Findings = append(p.Findings, Finding{b.Line, b.Err.Error()})
			}
			p.Blocks = append(p.Blocks, b)
		default:
			body = append(body, line)
		}
	}
	p.Body = strings.TrimSpace(strings.Join(body, "\n"))
	if p.ID == "" {
		p.ID = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	}
	if p.Title == "" {
		p.Title = p.ID
	}
	return p
}

// defGroup is a run of lines dropped because it starts like a link reference definition.
type defGroup struct{ line, n int }

// defLikeRe matches the start of a link reference definition: up to three spaces, a
// bracketed label, a colon. Nothing after the colon is looked at.
var defLikeRe = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:`)

// stripComments removes HTML comments from lines[start:] with one scan that carries the
// "inside a comment" state across lines (spec 7.4: comments are not indexed and never
// rendered). It returns the cleaned lines, which lines vanish entirely, and the runs of
// link-definition-like lines it dropped. Lines keep their positions so claim line numbers
// still point into the file.
//
//   - A complete claim comment line (see isCompleteClaimComment) is kept as it is, unless it
//     sits inside an open comment. A line that only starts like one goes through the scan.
//   - Text after a closing marker on the same line survives.
//   - A comment that is never closed hides everything after its opener.
//   - A line that starts like a link reference definition ("[label]: ...") is dropped together
//     with every following non-blank line. CommonMark lets such a definition render nothing
//     ("[//]: # (text)"), with a title that may run over several lines or sit on the next one,
//     and parsing titles exactly is a parser-differential risk. Over-stripping can only hide
//     visible text from the index, never reveal hidden text, and each run is reported.
//     Comment markers inside a dropped run are still scanned, because an HTML block can
//     interrupt a paragraph and run across blank lines.
func stripComments(lines []string, start int) (clean []string, dropped []bool, defs []defGroup) {
	clean = append([]string(nil), lines...)
	dropped = make([]bool, len(lines))
	inComment := false
	inDef := false
	for i := start; i < len(lines); i++ {
		if inComment {
			inDef = false
		} else if inDef && strings.TrimSpace(lines[i]) == "" {
			inDef = false
		}
		if !inComment && !inDef && defLikeRe.MatchString(lines[i]) {
			inDef = true
			defs = append(defs, defGroup{line: i + 1})
		}
		if inDef {
			defs[len(defs)-1].n++
			inComment = scanComments(lines[i], inComment, nil)
			clean[i], dropped[i] = "", true
			continue
		}
		if !inComment && isCompleteClaimComment(lines[i]) {
			continue
		}
		var out strings.Builder
		was := inComment
		inComment = scanComments(lines[i], inComment, &out)
		if was || inComment || out.String() != lines[i] {
			clean[i] = out.String()
			dropped[i] = strings.TrimSpace(clean[i]) == ""
		}
	}
	return clean, dropped, defs
}

// scanComments walks one line from the given comment state, writing the text outside comments
// to out (if not nil), and returns the state at the end of the line.
func scanComments(line string, inComment bool, out *strings.Builder) bool {
	rest := line
	for {
		if inComment {
			j := strings.Index(rest, "-->")
			if j < 0 {
				return true
			}
			rest, inComment = rest[j+len("-->"):], false
			continue
		}
		j := strings.Index(rest, "<!--")
		if j < 0 {
			if out != nil {
				out.WriteString(rest)
			}
			return false
		}
		if out != nil {
			out.WriteString(rest[:j])
		}
		rest, inComment = rest[j+len("<!--"):], true
	}
}

// isCompleteClaimComment reports whether a line is one whole claim comment: the claim prefix,
// one closing marker as the last thing on the line, and no other comment marker. Anything else
// that merely starts like a claim comment must go through the comment scan, or an unclosed
// opener would hide nothing from the index while Markdown hides everything after it.
func isCompleteClaimComment(line string) bool {
	s := strings.TrimSpace(line)
	return IsClaimComment(s) && strings.HasSuffix(s, "-->") &&
		strings.Count(s, "<!--") == 1 && strings.Count(s, "-->") == 1
}

func isHeading(s string) bool {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && len(s) > n && s[n] == ' '
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
