// Package redact replaces high-confidence secret shapes before text reaches the private
// store (spec 10.2, layer 1). It is a floor, not a guarantee.
package redact

import "regexp"

// A rule with a named group "secret" replaces only that span and keeps the rest of the
// match (the key, the separator, the URL host); a rule without one replaces the whole match.
type rule struct {
	kind string
	re   *regexp.Regexp
}

// keywords match inside an identifier (DB_PASSWORD, AWS_SECRET_ACCESS_KEY), so there is no
// leading word boundary; the optional tail lets the key name carry on to the separator.
const keywords = `(?:pass` + `word|passwd|sec` + `ret|tok` + `en|api[_-]?key)`

// Patterns are assembled from fragments so no full secret shape is a literal in source.
var rules = []rule{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*` + `PRIVATE KEY-----[\s\S]*?(-----END [A-Z0-9 ]*` + `PRIVATE KEY-----|\z)`)},
	{"aws-access-key", regexp.MustCompile(`\b(AK` + `IA|AS` + `IA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(gh[pousr]` + `_[A-Za-z0-9]{36,}|github_` + `pat_[A-Za-z0-9_]{40,})\b`)},
	{"api-key", regexp.MustCompile(`\b(sk|rk)` + `-[A-Za-z0-9_-]{20,}`)},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]` + `-[A-Za-z0-9-]{10,}`)},
	{"bearer", regexp.MustCompile(`(?i)\bbear` + `er\s+[A-Za-z0-9._~+/=-]{16,}`)},
	{"jwt", regexp.MustCompile(`\bey` + `J[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	// scheme://user:SECRET@host: the userinfo password of a connection URL. The password is
	// everything up to the last '@' of the token, so a '/', '+' or '@' inside it does not end
	// the match early; a URL with a later '@' in its path or query is redacted further than
	// necessary, which is the safe direction.
	{"url-credentials", regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s/@:]+:(?P<secret>\S+)@`)},
	// Authorization: Basic <base64>. The header name is required so that the word "basic"
	// in prose is left alone.
	{"basic-auth", regexp.MustCompile(`(?i)\bauthori` + `zation\s*[:=]\s*basic\s+(?P<secret>[A-Za-z0-9+/_-]{8,}={0,2})`)},
	// KEY=value, key: "value with spaces", "key": "value". Values under 8 characters are
	// left alone, which keeps prose such as "token: 5" intact.
	//
	// First rule: a key written as an identifier (the keyword alone or followed by _ or -, as in
	// DB_PASSWORD or password) followed by an unquoted value takes the rest of the line, up to
	// 200 characters, because a passphrase is several short words ("correct horse battery").
	// The key tail must not be plain letters, so prose such as "Passwords: never reuse them."
	// is left alone. Second rule: any key tail, a quoted value or one unquoted word of 8 or more.
	// A value already replaced is not replaced or counted again.
	{"assignment", regexp.MustCompile(`(?i)` + keywords + `(?:[_-][A-Za-z0-9_-]*)?["']?\s*[:=]\s*(?P<secret>[^\s"'][^\n]{7,199})`)},
	{"assignment", regexp.MustCompile(`(?i)` + keywords + `[A-Za-z0-9_-]*["']?\s*[:=]\s*(?P<secret>"[^"\n]{8,}"|'[^'\n]{8,}'|["']?[^\s"']{8,}["']?)`)},
}

// markerRe matches a value that is exactly one redaction marker. A value that only starts
// with one is attacker-controlled text and is redacted whole.
var markerRe = regexp.MustCompile(`^\[REDACTED:[a-z0-9-]+\]$`)

// String redacts s and returns counts of what was replaced, by kind.
func String(s string) (string, map[string]int) {
	counts := map[string]int{}
	for _, r := range rules {
		n := 0
		secret := r.re.SubexpIndex("secret")
		s = r.re.ReplaceAllStringFunc(s, func(m string) string {
			n++
			mark := "[REDACTED:" + r.kind + "]"
			if secret < 0 {
				return mark
			}
			loc := r.re.FindStringSubmatchIndex(m)
			if loc == nil || loc[2*secret] < 0 {
				return mark
			}
			if markerRe.MatchString(m[loc[2*secret]:loc[2*secret+1]]) {
				n-- // an earlier rule already replaced this whole value
				return m
			}
			return m[:loc[2*secret]] + mark + m[loc[2*secret+1]:]
		})
		if n > 0 {
			counts[r.kind] += n
		}
	}
	return s, counts
}
