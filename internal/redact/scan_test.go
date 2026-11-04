package redact

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestScanReportsOffsetsOfTheSecretOnly(t *testing.T) {
	key := "AK" + "IAABCDEFGHIJKLMNOP"
	in := "id: " + key + " end"
	spans := Scan(in)
	if len(spans) != 1 || spans[0].Kind != "aws-access-key" || in[spans[0].Start:spans[0].End] != key {
		t.Fatalf("spans = %+v", spans)
	}
	in2 := "x\n" + "DB_PASS" + "WORD=hunter2hunter2" + "\n"
	s2 := Scan(in2)
	if len(s2) != 1 || in2[s2[0].Start:s2[0].End] != "hunter2hunter2" {
		t.Fatalf("an assignment span must cover the value only: %+v", s2)
	}
}

func TestScanLeavesMarkersComparisonsAndProseAlone(t *testing.T) {
	for _, in := range []string{
		"pass" + "word=[REDACTED:assignment]",
		"if pass" + "word == other { }",
		"Pass" + "words: never reuse them.",
		"tok" + "en: 5",
		"",
	} {
		if sp := Scan(in); len(sp) != 0 {
			t.Errorf("Scan(%q) = %+v, want none", in, sp)
		}
	}
}

// Whatever String redacts, Scan must see. A source that String would change but Scan calls
// clean would be read by a subagent with its secret still in the file.
func TestScanSeesEverythingStringRedacts(t *testing.T) {
	cases := []string{
		"-----BEGIN RSA " + "PRIVATE KEY-----\nabc\n-----END RSA " + "PRIVATE KEY-----",
		"postgres://user:" + "p4ssw0rd-long" + "@db.example/x",
		"Author" + "ization: Basic " + base64.StdEncoding.EncodeToString([]byte("user:"+"x-pass-12345")),
		"api_" + "key: " + "\"" + "abcdefghijklmnop" + "\"",
		"gh" + "p_" + strings.Repeat("a", 36),
		"if pass" + "word == other: pass" + "word=SECRETVALUE1",
	}
	for _, in := range cases {
		out, counts := String(in)
		if out == in {
			t.Fatalf("fixture %q is not redacted by String; fix the fixture", in)
		}
		total := 0
		for _, n := range counts {
			total += n
		}
		if total > 0 && len(Scan(in)) == 0 {
			t.Errorf("String redacted %q but Scan found nothing", in)
		}
	}
}

func TestScanReportsEachPlaceOnce(t *testing.T) {
	in := "DB_PASS" + "WORD=hunter2hunter2"
	if sp := Scan(in); len(sp) != 1 {
		t.Fatalf("two overlapping rules must not double-count one secret: %+v", sp)
	}
}

func TestScanSpansAreOrderedAndInRange(t *testing.T) {
	in := "a=" + "AK" + "IAABCDEFGHIJKLMNOP and DB_PASS" + "WORD=hunter2hunter2"
	prev := -1
	for _, sp := range Scan(in) {
		if sp.Start < prev || sp.Start < 0 || sp.End > len(in) || sp.End <= sp.Start {
			t.Fatalf("bad span %+v in %d bytes", sp, len(in))
		}
		prev = sp.Start
	}
}
