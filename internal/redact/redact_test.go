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
