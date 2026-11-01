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
	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	p := Page{RelPath: rel, Type: "concept"}
	start := p.parseFrontmatter(lines)
	var body []string
	heading := ""
	for i := start; i < len(lines); i++ {
		line := lines[i]
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
				next := strings.TrimSpace(lines[i+1])
				if next == "" || IsClaimComment(next) || isHeading(next) {
					break
				}
				para = append(para, strings.TrimRight(lines[i+1], " \t"))
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
		case strings.HasPrefix(trimmed, "<!--"):
			// Non-claim comments are not indexed and never rendered (spec 7.4).
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
