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
