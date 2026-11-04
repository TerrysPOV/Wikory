package redact

import (
	"strings"
	"testing"
)

// PHP arrays and Ruby hash rockets write a key and its value with "=>". A quoted literal of 8 or
// more characters on the right, or a bare value that mixes letters and digits and is not followed
// by an expression character, is an assignment. Every secret is assembled from fragments.
func TestHashRocketAssignmentsAreRedactedAndScanned(t *testing.T) {
	cases := map[string]struct{ in, secret string }{
		"php array":         {"'db_pass" + "word' => 'Hunter2" + "Hunter2xyz',", "Hunter2" + "Hunter2xyz"},
		"php double quoted": {"\"api_" + "key\" => \"abcd" + "EFGH12345678\"", "abcd" + "EFGH12345678"},
		"ruby symbol":       {":sec" + "ret_key => \"abcd" + "EFGH12345678\"", "abcd" + "EFGH12345678"},
		"ruby bare":         {"pass" + "word => s3cr3t" + "valuehere", "s3cr3t" + "valuehere"},
		"ruby bare comma":   {"{ pass" + "word => s3cr3t" + "valuehere, user => 'x' }", "s3cr3t" + "valuehere"},
	}
	for name, c := range cases {
		out, counts := String(c.in)
		if strings.Contains(out, c.secret) || counts["assignment"] == 0 {
			t.Errorf("%s: String left the secret: %q -> %q %v", name, c.in, out, counts)
		}
		found := false
		for _, sp := range Scan(c.in) {
			if strings.Contains(c.in[sp.Start:sp.End], c.secret) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: Scan did not cover the secret in %q: %+v", name, c.in, Scan(c.in))
		}
	}
}

// A JavaScript arrow function, an expression on the right of "=>" and a plain identifier (no
// digit) stay clean, so ordinary code is not rewritten or skipped.
func TestArrowFunctionsAndExpressionsAfterARocketStayClean(t *testing.T) {
	for _, in := range []string{
		"tok" + "en => tok" + "en.trim()",
		"x => x.tok" + "en",
		"items.map(tok" + "en => tok" + "en.value)",
		"pass" + "word => some_function_name",
		"'pass" + "word' => env('DB_PASS" + "WORD'),",
		"'pass" + "word' => $pass" + "word,",
		"tok" + "enizer => tok" + "enizer12.parse(x)",
		"'pass" + "word' => 'short',",
	} {
		if out, c := String(in); out != in || len(c) != 0 {
			t.Errorf("String rewrote ordinary code: %q -> %q %v", in, out, c)
		}
		if sp := Scan(in); len(sp) != 0 {
			t.Errorf("Scan(%q) = %+v, want none", in, sp)
		}
	}
}
