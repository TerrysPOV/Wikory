// Package redact replaces high-confidence secret shapes before text reaches the private
// store (spec 10.2, layer 1). It is a floor, not a guarantee.
package redact

import "regexp"

type rule struct {
	kind string
	re   *regexp.Regexp
	repl string
}

// Patterns are assembled from fragments so no full secret shape is a literal in source.
var rules = []rule{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*` + `PRIVATE KEY-----[\s\S]*?(-----END [A-Z0-9 ]*` + `PRIVATE KEY-----|\z)`), ""},
	{"aws-access-key", regexp.MustCompile(`\b(AK` + `IA|AS` + `IA)[0-9A-Z]{16}\b`), ""},
	{"github-token", regexp.MustCompile(`\b(gh[pousr]` + `_[A-Za-z0-9]{36,}|github_` + `pat_[A-Za-z0-9_]{40,})\b`), ""},
	{"api-key", regexp.MustCompile(`\b(sk|rk)` + `-[A-Za-z0-9_-]{20,}`), ""},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]` + `-[A-Za-z0-9-]{10,}`), ""},
	{"bearer", regexp.MustCompile(`(?i)\bbear` + `er\s+[A-Za-z0-9._~+/=-]{16,}`), ""},
	{"jwt", regexp.MustCompile(`\bey` + `J[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), ""},
	{"assignment", regexp.MustCompile(`(?i)\b(pass` + `word|passwd|sec` + `ret|tok` + `en|api[_-]?key)(\s*[:=]\s*)["']?[^\s"']{8,}["']?`), "${1}${2}"},
}

// String redacts s and returns counts of what was replaced, by kind.
func String(s string) (string, map[string]int) {
	counts := map[string]int{}
	for _, r := range rules {
		n := 0
		s = r.re.ReplaceAllStringFunc(s, func(m string) string {
			n++
			prefix := ""
			if r.repl != "" {
				prefix = r.re.ReplaceAllString(m, r.repl)
			}
			return prefix + "[REDACTED:" + r.kind + "]"
		})
		if n > 0 {
			counts[r.kind] = n
		}
	}
	return s, counts
}
