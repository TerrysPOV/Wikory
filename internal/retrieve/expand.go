// Package retrieve serves memory: the per-checkout FTS5 index, search with computed
// freshness, and the SessionStart briefing (spec 7).
package retrieve

import (
	"regexp"
	"strings"
	"unicode"
)

var identRe = regexp.MustCompile(`[A-Za-z0-9_][A-Za-z0-9_./-]*`)

// splitIdent breaks an identifier or path into lower-case parts at separators and case changes.
func splitIdent(tok string) []string {
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(tok)
	for i, r := range rs {
		switch {
		case r == '_' || r == '.' || r == '/' || r == '-':
			flush()
			continue
		case i > 0 && unicode.IsUpper(r) && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])):
			flush()
		case i > 0 && unicode.IsUpper(r) && i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1]):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return parts
}

// Expand returns extra search words for every identifier and path in text: each part, and
// the parts joined, so enqueueJob, enqueue_job and "enqueue job" all match.
func Expand(text string) string {
	var out []string
	seen := map[string]bool{}
	add := func(w string) {
		if w != "" && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	for _, tok := range identRe.FindAllString(text, -1) {
		parts := splitIdent(tok)
		add(strings.Join(parts, ""))
		for _, p := range parts {
			add(p)
		}
	}
	return strings.Join(out, " ")
}

// QueryWords turns a free-text query into at most 32 plain words for an FTS5 OR query.
func QueryWords(q string) []string {
	words := strings.Fields(Expand(q))
	if len(words) > 32 {
		words = words[:32]
	}
	return words
}
