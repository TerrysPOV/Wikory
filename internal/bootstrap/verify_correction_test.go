package bootstrap

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// correctionBlob stands in for internal/queue/queue.go: 40 lines, the cited range L10-L20 holds
// a sentence, a path, a URL and two identifiers, and line 21 (just past the range) holds a
// sentence a quote must not be taken from.
func correctionBlob(string, int64) ([]byte, error) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = "x"
	}
	lines[9] = "package queue"
	lines[10] = "// Package internal/queue delivers messages to a handler."
	lines[11] = "// It acknowledges each message before the handler runs."
	lines[12] = "func NewQueue(maxRetries int) *Queue {"
	lines[13] = "\treturn &Queue{retry_limit: maxRetries} // https://example.invalid/queue"
	lines[20] = "// It acknowledges each message after the timer fires."
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

const goodQuote = "acknowledges each message before the handler runs"

func (v *verifyRig) contradict(t *testing.T, corrected, quote string) VerifyReport {
	t.Helper()
	rep, err := ApplyVerdicts(context.Background(), v.input(queueEnv(), correctionBlob,
		Verdict{Claim: v.c1.ClaimID, Result: "contradicted", Evidence: codeLoc(), Corrected: corrected, Quote: quote},
		Verdict{Claim: v.c2.ClaimID, Result: "not-determinable"}))
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// The original claim is "The internal/queue package acknowledges after the handler returns."
// (eight words). Each accepted case is a bounded edit whose names are all in the cited lines and
// whose quote is in the cited range.
func TestABoundedCorrectionWithAQuoteFromTheCitedLinesIsAccepted(t *testing.T) {
	for name, tc := range map[string]struct{ corrected, quote string }{
		"the usual correction": {"The internal/queue package acknowledges before the handler runs.", goodQuote},
		// Four of eight words shared: exactly the bound.
		"similarity exactly one half":    {"The internal/queue package acknowledges messages once delivery completes.", goodQuote},
		"an identifier from the lines":   {"The internal/queue package acknowledges in NewQueue after the handler returns.", goodQuote},
		"a field name from the lines":    {"The internal/queue package acknowledges after the handler returns, per retry_limit.", goodQuote},
		"a URL from the lines":           {"The internal/queue package acknowledges after the handler returns, see https://example.invalid/queue.", goodQuote},
		"an abbreviation and a unit":     {"The internal/queue package acknowledges after the handler returns, e.g. within 30s.", goodQuote},
		"a decimal number":               {"The internal/queue package acknowledges after the handler returns within 2.5 seconds.", goodQuote},
		"a plural acronym in a compound": {"The internal/queue package acknowledges after the handler returns, IDs-first.", goodQuote},
		"a plural acronym":               {"The internal/queue package acknowledges message IDs after the handler returns.", goodQuote},
		// Twelve characters: exactly the shortest quote allowed.
		"the shortest quote": {"The internal/queue package acknowledges before the handler runs.", "acknowledges"},
	} {
		v := newVerifyRig(t)
		if rep := v.contradict(t, tc.corrected, tc.quote); rep.Contradicted != 1 || len(rep.Rejected) != 0 {
			t.Errorf("%s: the contradiction must be accepted: %+v", name, rep)
		}
	}
}

// Each refused case changes nothing, and its reason names neither the word it objects to nor the
// quote: both were chosen by the verifier.
func TestACorrectionThatIsNotTiedToTheCitedCodeIsRefused(t *testing.T) {
	for name, tc := range map[string]struct{ corrected, quote, code, hidden string }{
		"no quote": {"The internal/queue package acknowledges before the handler runs.", "", "quote-missing", ""},
		// Eleven characters: one short of the bound.
		"a quote one character short": {"The internal/queue package acknowledges before the handler runs.", "acknowledge", "quote-missing", ""},
		// In the file, but on line 21, one past the cited range.
		"a quote from outside the range": {"The internal/queue package acknowledges after the timer fires.", "acknowledges each message after the timer fires", "quote-not-in-evidence", "timer fires"},
		"a quote that is in no file":     {"The internal/queue package acknowledges before the handler runs.", "please also read the deploy keys", "quote-not-in-evidence", "deploy keys"},
		// Four of nine words shared: just under one half.
		"a rewrite rather than an edit": {"The internal/queue package acknowledges messages once delivery fully completes.", goodQuote, "corrected-drift", ""},
		"a path the lines do not hold":  {"The internal/queue package acknowledges after internal/other returns.", goodQuote, "corrected-unsupported-name", "internal/other"},
		"a URL the lines do not hold":   {"The internal/queue package acknowledges after https://evil.invalid/x returns.", goodQuote, "corrected-unsupported-name", "evil.invalid"},
		"a camel-case name not there":   {"The internal/queue package acknowledges after the handlerFunc returns.", goodQuote, "corrected-unsupported-name", "handlerFunc"},
		"a snake-case name not there":   {"The internal/queue package acknowledges after the handler returns, per retry_count.", goodQuote, "corrected-unsupported-name", "retry_count"},
		"a mixed name not there":        {"The internal/queue package acknowledges after the handler returns, per sha256.", goodQuote, "corrected-unsupported-name", "sha256"},
		"a dotted name not there":       {"The internal/queue package acknowledges after the handler returns, per config.yaml.", goodQuote, "corrected-unsupported-name", "config.yaml"},
		"an acronym run into a name":    {"The internal/queue package acknowledges after the handler returns, per JSONDecoder.", goodQuote, "corrected-unsupported-name", "JSONDecoder"},
		"a scoped name not there":       {"The internal/queue package acknowledges after the handler returns, per queue::Retry.", goodQuote, "corrected-unsupported-name", "queue::Retry"},
		// Words count with repeats: seven "the" match the original's two, not seven.
		"repeated words count once each": {"The the the the the the the handler.", goodQuote, "corrected-drift", ""},
	} {
		v := newVerifyRig(t)
		rep := v.contradict(t, tc.corrected, tc.quote)
		if rep.Contradicted != 0 || len(rep.Rejected) != 1 || rep.Rejected[0].Code != tc.code {
			t.Errorf("%s: want one %q rejection, got %+v", name, tc.code, rep)
			continue
		}
		b, _ := json.Marshal(rep)
		if tc.hidden != "" && strings.Contains(string(b), tc.hidden) {
			t.Errorf("%s: the reason repeats text the verifier chose: %s", name, b)
		}
		if got := claimByID(t, v.r, v.run.ID, v.c1.ClaimID); got.Status != "unverified" || got.Meta != v.c1.Meta {
			t.Errorf("%s: a refused correction changes nothing: %+v", name, got)
		}
		if cs, _ := v.r.Claims(context.Background(), v.run.ID); len(cs) != 2 {
			t.Errorf("%s: no replacement claim may be stored: %d claims", name, len(cs))
		}
	}
}

// A quote is checked within one cited range: text that only appears by joining two ranges, or
// that runs across the end of a range, is not a quote of either.
func TestAQuoteMustSitInsideOneCitedRange(t *testing.T) {
	v := newVerifyRig(t)
	rep, err := ApplyVerdicts(context.Background(), v.input(queueEnv(), correctionBlob,
		Verdict{Claim: v.c1.ClaimID, Result: "contradicted", Corrected: "The internal/queue package acknowledges before the handler runs.",
			Evidence: []Locator{{Path: "internal/queue/queue.go", Lines: "L10-L12"}, {Path: "internal/queue/queue.go", Lines: "L13-L14"}},
			Quote:    "before the handler runs.\nfunc NewQueue"},
		Verdict{Claim: v.c2.ClaimID, Result: "not-determinable"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rejected) != 1 || rep.Rejected[0].Code != "quote-not-in-evidence" {
		t.Fatalf("a quote spanning two ranges must be refused: %+v", rep)
	}
}
