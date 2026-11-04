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
	// The second fixture has text after the value, so the two assignment rules end at
	// different offsets (one takes the rest of the line, one a single word) from one Start.
	for _, tc := range []struct{ in, want string }{
		{"DB_PASS" + "WORD=hunter2hunter2", "hunter2hunter2"},
		{"DB_PASS" + "WORD=hunter2hunter2 # note here", "hunter2hunter2 # note here"},
	} {
		sp := Scan(tc.in)
		if len(sp) != 1 {
			t.Fatalf("one secret in %q must be one span, got %+v", tc.in, sp)
		}
		if got := tc.in[sp[0].Start:sp[0].End]; got != tc.want {
			t.Errorf("span covers %q, want the widest match %q", got, tc.want)
		}
	}
}

func TestScanSpansAreOrderedAndInRange(t *testing.T) {
	// The assignment comes first in the text but later in the rule list, so rule order and
	// position order disagree and only an explicit sort puts the spans in position order.
	in := "DB_PASS" + "WORD=hunter2hunter2\n" + "AK" + "IAABCDEFGHIJKLMNOP"
	spans := Scan(in)
	if len(spans) != 2 {
		t.Fatalf("want 2 spans, got %+v", spans)
	}
	if spans[0].Kind != "assignment" || spans[1].Kind != "aws-access-key" {
		t.Fatalf("spans out of position order: %+v", spans)
	}
	prev := -1
	for _, sp := range spans {
		if sp.Start < prev || sp.Start < 0 || sp.End > len(in) || sp.End <= sp.Start {
			t.Fatalf("bad span %+v in %d bytes", sp, len(in))
		}
		prev = sp.Start
	}
}

func TestScanUnionsAPartiallyOverlappingTail(t *testing.T) {
	// The assignment value is capped at 200 characters, so it ends inside the key: the sk-
	// rule starts at offset 160 (inside the value) and ends at 263 (after it). Neither span
	// is nested in the other, so only the extend branch keeps the key's tail in the result.
	in := "PASS" + "WORD=" + strings.Repeat("a", 150) + " sk" + "-" + strings.Repeat("B", 100)
	const wantStart, wantEnd = 9, 263
	spans := Scan(in)
	if len(spans) != 1 {
		t.Fatalf("partial overlap must merge into one span, got %+v", spans)
	}
	if spans[0].Start != wantStart || spans[0].End != wantEnd {
		t.Errorf("partial overlap span = [%d,%d), want [%d,%d)", spans[0].Start, spans[0].End, wantStart, wantEnd)
	}
	if !strings.HasSuffix(in[spans[0].Start:spans[0].End], strings.Repeat("B", 100)) {
		t.Errorf("merged span drops the tail of the key")
	}
}
