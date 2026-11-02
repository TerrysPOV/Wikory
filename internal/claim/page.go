package claim

import (
	"fmt"
	"path"
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
	clean, dropped := stripComments(lines, start)
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

// stripComments removes HTML comments from lines[start:] with one scan that carries the
// "inside a comment" state across lines (spec 7.4: comments are not indexed and never
// rendered). It returns the cleaned lines and which lines vanish entirely. Lines keep their
// positions so claim line numbers still point into the file.
//
//   - A complete claim comment line (see isCompleteClaimComment) is kept as it is, unless it
//     sits inside an open comment. A line that only starts like one goes through the scan.
//   - Text after a closing marker on the same line survives.
//   - A comment that is never closed hides everything after its opener.
//   - CommonMark link reference definitions of the form "[//]: # (text)" and "[//]: <> (text)"
//     render nothing, so they are removed too (a title may run over several lines).
func stripComments(lines []string, start int) (clean []string, dropped []bool) {
	clean = append([]string(nil), lines...)
	dropped = make([]bool, len(lines))
	inComment := false
	closer := byte(0) // the closing delimiter of an open [//]: title, or 0
	for i := start; i < len(lines); i++ {
		if closer != 0 {
			if strings.TrimSpace(lines[i]) == "" { // a blank line ends the definition
				closer = 0
			} else {
				clean[i], dropped[i] = "", true
				if strings.IndexByte(lines[i], closer) >= 0 {
					closer = 0
				}
				continue
			}
		}
		if !inComment && isCompleteClaimComment(lines[i]) {
			continue
		}
		if !inComment {
			if isHidden, open := commentRefDef(lines[i]); isHidden {
				clean[i], dropped[i], closer = "", true, open
				continue
			}
		}
		var out strings.Builder
		rest, touched := lines[i], inComment
		for {
			if inComment {
				j := strings.Index(rest, "-->")
				if j < 0 {
					break
				}
				rest, inComment = rest[j+len("-->"):], false
				continue
			}
			j := strings.Index(rest, "<!--")
			if j < 0 {
				out.WriteString(rest)
				break
			}
			out.WriteString(rest[:j])
			rest, inComment, touched = rest[j+len("<!--"):], true, true
		}
		if touched {
			clean[i] = out.String()
			dropped[i] = strings.TrimSpace(clean[i]) == ""
		}
	}
	return clean, dropped
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

// commentRefDef recognises a link reference definition used as an invisible comment:
// "[//]: # (text)" or "[//]: <> (text)" (also with "text" or 'text'), indented at most three
// spaces. If its title is opened but not closed on this line, open is the closing delimiter
// to look for on the following lines.
func commentRefDef(line string) (hidden bool, open byte) {
	if len(line)-len(strings.TrimLeft(line, " ")) > 3 {
		return false, 0
	}
	s := strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(s, "[//]:")
	if !ok {
		return false, 0
	}
	rest = strings.TrimLeft(rest, " \t")
	switch {
	case strings.HasPrefix(rest, "<>"):
		rest = rest[2:]
	case strings.HasPrefix(rest, "#"):
		rest = rest[1:]
	default:
		return false, 0
	}
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return false, 0 // "[//]: #foo" is a real destination, not the comment idiom
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return true, 0
	}
	var end byte
	switch rest[0] {
	case '(':
		end = ')'
	case '"', '\'':
		end = rest[0]
	default:
		return false, 0
	}
	if strings.IndexByte(rest[1:], end) >= 0 {
		return true, 0
	}
	return true, end
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
