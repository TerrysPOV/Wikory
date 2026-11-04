// Package redact replaces high-confidence secret shapes before text reaches the private
// store (spec 10.2, layer 1). It is a floor, not a guarantee.
package redact

import (
	"regexp"
	"strings"
	"unicode"
)

// A rule with a named group "secret" replaces only that span and keeps the rest of the
// match (the key, the separator, the URL host); a rule without one replaces the whole match.
//
// accept, when set, is asked about every "secret" span the rule finds; a span it refuses is left
// alone, like a comparison.
type rule struct {
	kind   string
	re     *regexp.Regexp
	accept func(secret string) bool
}

// rocketValue accepts a quoted literal, or a bare value that mixes letters and digits. A bare
// identifier with no digit ("some_function_name", "tokenList") is far more often code than a
// secret, so it is left alone after "=>".
func rocketValue(v string) bool {
	if v[0] == '"' || v[0] == '\'' {
		return true
	}
	return strings.ContainsAny(v, "0123456789") && strings.IndexFunc(v, unicode.IsLetter) >= 0
}

// keywords match inside an identifier (DB_PASSWORD, AWS_SECRET_ACCESS_KEY), so there is no
// leading word boundary; the optional tail lets the key name carry on to the separator.
const keywords = `(?:pass` + `word|passwd|sec` + `ret|tok` + `en|api[_-]?key)`

// Patterns are assembled from fragments so no full secret shape is a literal in source.
var rules = []rule{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*` + `PRIVATE KEY-----[\s\S]*?(-----END [A-Z0-9 ]*` + `PRIVATE KEY-----|\z)`), nil},
	{"aws-access-key", regexp.MustCompile(`\b(AK` + `IA|AS` + `IA)[0-9A-Z]{16}\b`), nil},
	{"github-token", regexp.MustCompile(`\b(gh[pousr]` + `_[A-Za-z0-9]{36,}|github_` + `pat_[A-Za-z0-9_]{40,})\b`), nil},
	{"api-key", regexp.MustCompile(`\b(sk|rk)` + `-[A-Za-z0-9_-]{20,}`), nil},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]` + `-[A-Za-z0-9-]{10,}`), nil},
	{"bearer", regexp.MustCompile(`(?i)\bbear` + `er\s+[A-Za-z0-9._~+/=-]{16,}`), nil},
	{"jwt", regexp.MustCompile(`\bey` + `J[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), nil},
	// scheme://user:SECRET@host: the userinfo password of a connection URL. The password is
	// everything up to the last '@' of the token, so a '/', '+' or '@' inside it does not end
	// the match early; a URL with a later '@' in its path or query is redacted further than
	// necessary, which is the safe direction.
	{"url-credentials", regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s/@:]*:(?P<secret>\S+)@`), nil},
	// Authorization: Basic <base64>. The header name is required so that the word "basic"
	// in prose is left alone.
	{"basic-auth", regexp.MustCompile(`(?i)\bauthori` + `zation\s*[:=]\s*basic\s+(?P<secret>[A-Za-z0-9+/_-]{8,}={0,2})`), nil},
	// KEY=value, key: "value with spaces", "key": "value". Values under 8 characters are
	// left alone, which keeps prose such as "token: 5" intact.
	//
	// First rule: a key written as an identifier (the keyword alone or followed by _ or -, as in
	// DB_PASSWORD or password) followed by an unquoted value takes the rest of the line, up to
	// 200 characters, because a passphrase is several short words ("correct horse battery").
	// The key tail must not be plain letters, so prose such as "Passwords: never reuse them."
	// is left alone. Second rule: any key tail, a quoted value or one unquoted word of 8 or more.
	// A value already replaced is not replaced or counted again. The operator is ":", "=" or
	// ":=" and the value may start with any character, so "=~secret" and "==secret" are
	// assignments. A comparison written with spaces ("password == other", "token => name",
	// "password =~ /re/") is a named group "cmp" that consumes the operator and the one operand
	// after it and is left alone, so ordinary code is not rewritten and scanning resumes right
	// after the operand ("if password == other: password=SECRET" still redacts the second).
	// "!=", "<=" and ">=" never reach the operator.
	{"assignment", regexp.MustCompile(`(?i)` + keywords + `(?:[_-][A-Za-z0-9_-]*)?["']?\s*(?:(?P<cmp>(?:==|=>|=~)[ \t]+\S*)|(?::=|[:=])\s*(?P<secret>[^\s"'][^\n]{7,199}))`), nil},
	{"assignment", regexp.MustCompile(`(?i)` + keywords + `[A-Za-z0-9_-]*["']?\s*(?:(?P<cmp>(?:==|=>|=~)[ \t]+\S*)|(?::=|[:=])\s*(?P<secret>"[^"\n]{8,}"|'[^'\n]{8,}'|["']?[^\s"']{8,}["']?))`), nil},
	// A PHP array entry or Ruby hash rocket: 'db_password' => 'value', :secret_key => "value",
	// password => value. The two rules above treat "=>" with spaces as a comparison; this one
	// takes it as an assignment when the right side is a quoted literal of 8 or more characters,
	// or a bare value of 8 or more that holds no expression character, ends at a space, a
	// separator or the end of the text (so "x1234567.trim()" is an expression), and mixes letters
	// and digits (see rocketValue). A JavaScript arrow such as "token => token.trim()" is left
	// alone.
	{"assignment", regexp.MustCompile(`(?i)` + keywords + `[A-Za-z0-9_-]*["']?\s*=>[ \t]*(?P<secret>"[^"\n]{8,}"|'[^'\n]{8,}'|[^\s"'.()\[\]{},;<>=!&|$]{8,})(?:["'\s,;)\]}]|\z)`), rocketValue},
}

// markerRe matches a value that is exactly one redaction marker. Markers in the input are
// rewritten before any rule runs (see String), so a marker seen by a rule was emitted by an
// earlier rule in the same call, and a value that is exactly one is not redacted again.
var markerRe = regexp.MustCompile(`^\[REDACTED:[a-z0-9-]+\]$`)

// String redacts s and returns counts of what was replaced, by kind.
//
// A redaction marker already present in s is not trusted: it is rewritten to "[REDACTED-IN:"
// first, so no input can pose as the output of a rule or hide the text beside it.
func String(s string) (string, map[string]int) {
	counts := map[string]int{}
	s = strings.ReplaceAll(s, "[REDACTED:", "[REDACTED-IN:")
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
			if cmp := r.re.SubexpIndex("cmp"); cmp >= 0 && loc != nil && loc[2*cmp] >= 0 {
				n-- // a comparison, not an assignment
				return m
			}
			if loc == nil || loc[2*secret] < 0 {
				return mark
			}
			if r.accept != nil && !r.accept(m[loc[2*secret]:loc[2*secret+1]]) {
				n-- // not a value this rule takes as a secret
				return m
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
