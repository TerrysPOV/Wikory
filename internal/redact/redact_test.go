package redact

import (
	"strings"
	"testing"
)

func TestRedactsEachKind(t *testing.T) {
	cases := map[string]string{
		"aws-access-key": "key " + "AK" + "IA" + strings.Repeat("Q", 16) + " end",
		"github-token":   "tok " + "gh" + "p_" + strings.Repeat("a", 36) + " end",
		"api-key":        "use " + "sk" + "-" + strings.Repeat("b", 32) + " end",
		"slack-token":    "x " + "xo" + "xb-" + strings.Repeat("1", 12) + " end",
		"jwt":            "jwt " + "ey" + "J" + strings.Repeat("c", 12) + "." + strings.Repeat("d", 12) + "." + strings.Repeat("e", 12) + " end",
		"private-key":    "-----BEGIN " + "RSA PRIVATE KEY-----\nMIIBOg\n-----END " + "RSA PRIVATE KEY-----",
		"assignment":     "pass" + "word = hunter2hunter2",
		"bearer":         "Authorization: " + "Bear" + "er " + strings.Repeat("R", 32),
	}
	for kind, in := range cases {
		out, counts := String(in)
		if counts[kind] != 1 || !strings.Contains(out, "[REDACTED:"+kind+"]") {
			t.Errorf("%s: out=%q counts=%v", kind, out, counts)
		}
		if strings.Contains(out, strings.Repeat("Q", 16)) || strings.Contains(out, "hunter2hunter2") || strings.Contains(out, "MIIBOg") {
			t.Errorf("%s: secret material survived: %q", kind, out)
		}
	}
}

func TestLeavesOrdinaryTextAlone(t *testing.T) {
	in := "The token bucket refills every second; set password policy in config. See sk-learn docs."
	out, counts := String(in)
	if out != in || len(counts) != 0 {
		t.Fatalf("false positive: %q %v", out, counts)
	}
}

// Secret-shaped strings below are assembled from fragments at runtime; none is a literal.
var (
	kwPassword = "PASS" + "WORD"
	kwSecret   = "SEC" + "RET"
	kwToken    = "TOK" + "EN"
	kwAPIKey   = "API" + "_KEY"
	value      = "Zx9" + "kQ2mP" + "7vL4"
)

func TestRedactsKeywordInsideIdentifier(t *testing.T) {
	names := []string{
		"DB_" + kwPassword,
		"AWS_" + kwSecret + "_ACCESS_KEY",
		"STRIPE_" + kwAPIKey,
		"GITHUB_" + kwToken,
		"db_" + strings.ToLower(kwPassword),
		"Mixed" + kwSecret + "Name",
	}
	for _, name := range names {
		in := "export " + name + "=" + value + " done"
		out, counts := String(in)
		if strings.Contains(out, value) || counts["assignment"] != 1 || !strings.Contains(out, name+"=[REDACTED:assignment]") {
			t.Errorf("%s: out=%q counts=%v", name, out, counts)
		}
	}
}

func TestRedactsURLUserinfoPassword(t *testing.T) {
	in := "dsn postgres://app_user:" + value + "@db.internal:5432/app?sslmode=require end"
	out, counts := String(in)
	if strings.Contains(out, value) || counts["url-credentials"] != 1 ||
		!strings.Contains(out, "postgres://app_user:[REDACTED:url-credentials]@db.internal:5432/app") {
		t.Fatalf("out=%q counts=%v", out, counts)
	}
	for _, plain := range []string{"see https://example.com:8080/path", "mail me at https://example.com/a:b@c", "clone ssh://git@github.com/org/repo"} {
		if got, c := String(plain); got != plain || len(c) != 0 {
			t.Errorf("false positive: %q -> %q %v", plain, got, c)
		}
	}
}

func TestRedactsBasicAuthorizationHeader(t *testing.T) {
	b64 := "dXNlcjo" + "6aHVudGVyMg=="
	for _, in := range []string{
		"Authorization: Basic " + b64,
		"curl -H 'authorization:basic " + b64 + "' https://x.invalid",
		"Proxy-Authorization: Basic " + b64,
	} {
		out, counts := String(in)
		if strings.Contains(out, b64) || counts["basic-auth"] != 1 {
			t.Errorf("%q -> %q %v", in, out, counts)
		}
		if !strings.Contains(strings.ToLower(out), "basic [redacted:basic-auth]") {
			t.Errorf("the scheme name should stay: %q", out)
		}
	}
	if out, c := String("The basic authentication flow is described below."); out != "The basic authentication flow is described below." || len(c) != 0 {
		t.Errorf("prose mangled: %q %v", out, c)
	}
}

func TestRedactsQuotedValuesWithSpaces(t *testing.T) {
	kw := strings.ToLower(kwPassword)
	for _, in := range []string{
		kw + `: "correct horse battery"`,
		kw + `= 'correct horse battery'`,
		`{"` + kw + `": "correct horse battery"}`,
	} {
		out, counts := String(in)
		if strings.Contains(out, "horse") || strings.Contains(out, "battery") || counts["assignment"] != 1 {
			t.Errorf("%q -> %q %v", in, out, counts)
		}
	}
	// A second quoted phrase later on the line must survive: the value ends at its own closing quote.
	out, _ := String(kw + `: "correct horse battery" and then "more" text`)
	if !strings.HasSuffix(out, ` and then "more" text`) {
		t.Errorf("text after the closing quote was swallowed: %q", out)
	}
}

// The wider key rule must not turn ordinary prose into a pile of redaction markers.
func TestKeywordRuleLeavesProseAlone(t *testing.T) {
	for _, in := range []string{
		"the password reset page",
		"Rotate the token every 90 days and review the secret store.",
		"max tokens: 5, password: short",
		"This token bucket refills; see the api-key rotation runbook for details.",
		"Use a password manager. Passwords: never reuse them.",
	} {
		if out, c := String(in); out != in || len(c) != 0 {
			t.Errorf("prose mangled: %q -> %q %v", in, out, c)
		}
	}
}

// Review M2: a connection URL with an empty username (redis://:SECRET@host) is common.
func TestRedactsURLPasswordWithAnEmptyUsername(t *testing.T) {
	pw := "Zq8" + "kW3mR" + "7vN2"
	in := "cache redis://:" + pw + "@cache.internal:6379 end"
	out, counts := String(in)
	if strings.Contains(out, pw) || counts["url-credentials"] != 1 || !strings.Contains(out, "redis://:[REDACTED:url-credentials]@cache.internal:6379") {
		t.Fatalf("out=%q counts=%v", out, counts)
	}
}

// Review L2: comparison and match operators are not assignments, so ordinary code that tests
// a keyword-named variable is not rewritten; single-character assignment forms still are.
func TestComparisonOperatorsAreNotAssignments(t *testing.T) {
	kw := strings.ToLower(kwPassword)
	tk := strings.ToLower(kwToken)
	for _, in := range []string{
		"if " + kw + " == expected_value_here",
		"if " + tk + " != previous_value_here",
		"while " + kw + " <= limit_value_here",
		"while " + kw + " >= other_value_here",
		kw + " => some_function_name",
		kw + " =~ regex_pattern_here",
		"if " + kw + " == expected_" + kw + ": return True",
		"x = " + kw + " =~ /re/",
	} {
		if out, c := String(in); out != in || len(c) != 0 {
			t.Errorf("a comparison was rewritten: %q -> %q %v", in, out, c)
		}
	}
	for _, in := range []string{kw + " = hunter2hunter2", kw + "=hunter2hunter2", kw + ": hunter2hunter2", tk + " := hunter2hunter2", tk + " := correct horse battery staple"} {
		if out, c := String(in); strings.Contains(out, "hunter2") || strings.Contains(out, "horse") || len(c) == 0 {
			t.Errorf("an assignment survived: %q -> %q %v", in, out, c)
		}
	}
}

// A real secret may start with an operator character. After a single "=" or ":" the value is
// anything at all, so these are assignments and are redacted. Secrets are built at runtime.
func TestValuesStartingWithOperatorCharactersAreRedacted(t *testing.T) {
	kw := strings.ToLower(kwPassword)
	tk := strings.ToLower(kwToken)
	tail := "hunter2" + "hunter2"
	for _, in := range []string{
		kw + "=~" + tail,
		tk + "=>" + tail,
		"key_" + kw + "==" + tail,
		kw + ":~" + tail,
		kw + ": >" + tail,
		kw + " = =" + tail,
		kw + "=~" + tail + " and more words",
		tk + "=>correct horse battery staple",
	} {
		out, c := String(in)
		if strings.Contains(out, "hunter2") || strings.Contains(out, "horse") || c["assignment"] != 1 {
			t.Errorf("a secret behind an operator character survived: %q -> %q %v", in, out, c)
		}
	}
}

// A fine-grained GitHub token has its own prefix and underscores in the body; only the
// github_pat_ alternative of the github-token rule can see it.
func TestRedactsFineGrainedGitHubToken(t *testing.T) {
	body := strings.Repeat("A", 22) + "_" + strings.Repeat("b", 59)
	in := "use " + "github_" + "pat_" + body + " end"
	out, counts := String(in)
	if counts["github-token"] != 1 || !strings.Contains(out, "[REDACTED:github-token]") || strings.Contains(out, body) {
		t.Fatalf("fine-grained token survived: out=%q counts=%v", out, counts)
	}
}

// The api-key rule starts at 20 characters after the prefix. The existing 32 character input
// cannot see that bound move; 20 and 25 characters can, and 19 must stay as prose.
func TestAPIKeyMinimumLengthIsTwenty(t *testing.T) {
	for _, n := range []int{20, 25} {
		body := strings.Repeat("k", n)
		in := "use " + "sk" + "-" + body + " end"
		out, counts := String(in)
		if counts["api-key"] != 1 || strings.Contains(out, body) {
			t.Errorf("%d characters: out=%q counts=%v", n, out, counts)
		}
	}
	short := "use " + "sk" + "-" + strings.Repeat("k", 19) + " end"
	if out, counts := String(short); out != short || len(counts) != 0 {
		t.Errorf("19 characters were redacted: out=%q counts=%v", out, counts)
	}
}

// Review L4: a connection URL whose userinfo password holds '/', '+' or '@' used to stop the
// match at the first of them and leave the rest of the password in the text. The passwords
// are assembled from fragments at runtime.
func TestRedactsURLPasswordsContainingSlashPlusAndAt(t *testing.T) {
	for _, pw := range []string{"Ab/cd" + "+ef9Gh", "pa/ss" + "/wo/rd1", "x+y" + "+z+Q7wq", "p@ss" + "w0rd@x1"} {
		in := "dsn postgres://app_user:" + pw + "@db.internal:5432/app end"
		out, counts := String(in)
		if counts["url-credentials"] != 1 || !strings.HasSuffix(out, "[REDACTED:url-credentials]@db.internal:5432/app end") {
			t.Errorf("pw %d chars: out=%q counts=%v", len(pw), out, counts)
		}
		for _, frag := range strings.FieldsFunc(pw, func(r rune) bool { return r == '/' || r == '+' || r == '@' }) {
			if len(frag) >= 4 && strings.Contains(out, frag) {
				t.Errorf("part %q of the password survived: %q", frag, out)
			}
		}
	}
}

// Redaction markers that arrive in the input are not trusted at all: they are rewritten to a
// visibly different form before any rule runs, so no input can pose as output of a rule.
func TestMarkersInTheInputAreNeutralisedAndNeverTrusted(t *testing.T) {
	kw := strings.ToLower(kwPassword)
	tail := "real" + "secret" + "value123"
	b64 := "dXNlcjo" + "6aHVudGVyMg=="
	for name, in := range map[string]string{
		"key=value":        kw + "=[REDACTED:x]" + tail,
		"key: value":       kw + ": [REDACTED:assignment]" + tail,
		"header after":     "[REDACTED:x]Authorization: Basic " + b64,
		"url after marker": "[REDACTED:x]postgres://u:" + tail + "@h/db",
	} {
		out, counts := String(in)
		if strings.Contains(out, tail) || strings.Contains(out, b64) {
			t.Errorf("%s: real secret text survived: %q", name, out)
		}
		total := 0
		for _, n := range counts {
			total += n
		}
		if total != 1 {
			t.Errorf("%s: counts %v for %q", name, counts, out)
		}
	}
	out, counts := String("see [REDACTED:api-key] in the log")
	if out != "see [REDACTED-IN:api-key] in the log" || len(counts) != 0 {
		t.Errorf("an input marker was not neutralised: %q %v", out, counts)
	}
}

// A value that merely starts with a redaction marker is attacker-controlled text and is
// redacted whole (the input marker is neutralised first; see the test above).
func TestOnlyAWholeMarkerCountsAsAlreadyRedacted(t *testing.T) {
	kw := strings.ToLower(kwPassword)
	tail := "real" + "secret" + "value123"
	for _, in := range []string{
		kw + "=[REDACTED:x]" + tail,
		kw + ": [REDACTED:x]" + tail,
		kw + ": [REDACTED:x] " + tail + " more words",
		strings.ToUpper(kw) + "_X=[REDACTED:api-key]" + tail,
	} {
		out, counts := String(in)
		if strings.Contains(out, tail) {
			t.Errorf("the real value survived behind a forged marker: %q -> %q", in, out)
		}
		if counts["assignment"] != 1 {
			t.Errorf("%q: counts %v", in, counts)
		}
	}
}

// Review L4: only the first word of an unquoted value used to be tested against the eight
// character minimum, so a passphrase of short words was left in the text.
func TestRedactsMultiWordUnquotedValues(t *testing.T) {
	kw := strings.ToLower(kwPassword)
	phrase := "correct horse battery staple"
	for _, in := range []string{
		kw + ": " + phrase,
		kw + " = " + phrase,
		strings.ToUpper(kw) + "_FILE=" + phrase + " and more words",
		"the " + kw + ": " + phrase,
	} {
		out, counts := String(in)
		for _, w := range []string{"correct", "horse", "battery", "staple"} {
			if strings.Contains(out, w) {
				t.Errorf("%q survived in %q", w, out)
			}
		}
		if counts["assignment"] != 1 {
			t.Errorf("%q: counts %v", out, counts)
		}
	}
	// The redaction is bounded: past 200 characters the line is left alone.
	long := kw + ": " + strings.Repeat("w", 199) + "  " + strings.Repeat("TAIL ", 20)
	if out, _ := String(long); !strings.Contains(out, "TAIL TAIL") || !strings.Contains(out, "[REDACTED:assignment]") {
		t.Errorf("unbounded or missed: %q", out)
	}
}
