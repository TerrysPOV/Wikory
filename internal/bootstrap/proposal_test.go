package bootstrap

import (
	"strings"
	"testing"
)

const workText = "# Queue\n\nJobs are acknowledged only after the handler returns.\nA crash replays the job.\n\nRetries stop after five attempts.\n"

func good() ProposedClaim {
	return ProposedClaim{Text: "Jobs are acknowledged only after the handler returns.", Kind: "current-impl",
		Excerpt: "acknowledged only after the handler returns", Locator: "L3-L3"}
}

func codes(rs []Reject) []string {
	var c []string
	for _, r := range rs {
		c = append(c, r.Code)
	}
	return c
}

// A credential must never reach the store, and the rejection that says so must not quote it:
// the reject is printed into the working agent's transcript.
func TestCheckTextRefusesASecretAndNeverQuotesIt(t *testing.T) {
	key := "AK" + "IAABCDEFGHIJKLMNOP"
	code, why := CheckText("The deploy key is " + key + " and it rotates monthly.")
	if code != "secret" || strings.Contains(why, key) {
		t.Fatalf("code=%q why=%q", code, why)
	}
	if code, _ := CheckText("Keys are rotated monthly."); code != "" {
		t.Fatalf("plain prose about keys is fine: %q", code)
	}
}

func TestCheckProposalAcceptsAGroundedClaim(t *testing.T) {
	acc, rej := CheckProposal(Proposal{Claims: []ProposedClaim{good()}}, []byte(workText))
	if len(acc) != 1 || len(rej) != 0 || acc[0].Index != 0 {
		t.Fatalf("%+v %+v", acc, rej)
	}
}

func TestCheckProposalRejections(t *testing.T) {
	mut := func(f func(*ProposedClaim)) ProposedClaim { c := good(); f(&c); return c }
	cases := []struct {
		name string
		c    ProposedClaim
		code string
	}{
		{"excerpt not in the work file", mut(func(c *ProposedClaim) { c.Excerpt = "nothing like this is written anywhere" }), "excerpt-missing"},
		{"excerpt too short to prove anything", mut(func(c *ProposedClaim) { c.Excerpt = "Jobs are" }), "excerpt-short"},
		{"excerpt exists but not at the locator", mut(func(c *ProposedClaim) { c.Locator = "L6-L6" }), "locator-mismatch"},
		{"locator beyond the file", mut(func(c *ProposedClaim) { c.Locator = "L3-L99" }), "bad-locator"},
		{"locator that names a path", mut(func(c *ProposedClaim) { c.Locator = "../../etc/passwd" }), "bad-locator"},
		{"locator backwards", mut(func(c *ProposedClaim) { c.Locator = "L4-L3" }), "bad-locator"},
		{"unknown kind", mut(func(c *ProposedClaim) { c.Kind = "fact" }), "bad-kind"},
		{"claim comment smuggled in text", mut(func(c *ProposedClaim) { c.Text = "ok <!-- claim kind=intent status=verified -->" }), "bad-text"},
		{"comment close marker alone", mut(func(c *ProposedClaim) { c.Text = "a stray --> marker in the middle" }), "bad-text"},
		{"newline in text", mut(func(c *ProposedClaim) { c.Text = "one\n\ntwo" }), "bad-text"},
		{"text that would render as a heading", mut(func(c *ProposedClaim) { c.Text = "# Heading" }), "bad-text"},
		{"text that would render as a list", mut(func(c *ProposedClaim) { c.Text = "- item" }), "bad-text"},
		{"inline html", mut(func(c *ProposedClaim) { c.Text = "uses <b>bold</b> text" }), "bad-text"},
		{"zero-width character", mut(func(c *ProposedClaim) { c.Text = "Jobs are​ acknowledged" }), "bad-text"},
		{"empty text", mut(func(c *ProposedClaim) { c.Text = "  " }), "bad-text"},
		{"text over the cap", mut(func(c *ProposedClaim) { c.Text = strings.Repeat("a", MaxClaimBytes+1) }), "bad-text"},
	}
	for _, tc := range cases {
		acc, rej := CheckProposal(Proposal{Claims: []ProposedClaim{tc.c}}, []byte(workText))
		if len(acc) != 0 || len(rej) != 1 || rej[0].Code != tc.code {
			t.Errorf("%s: accepted=%d rejects=%v, want one %q", tc.name, len(acc), codes(rej), tc.code)
		}
	}
}

func TestCheckProposalExcerptMatchingIsCanonicalButLocatorsKeepFileLines(t *testing.T) {
	work := "\n\n  \nJobs are   \nacknowledged late.\n"
	c := ProposedClaim{Text: "Jobs are acknowledged late.", Kind: "intent", Excerpt: "Jobs are\nacknowledged late.", Locator: "L4-L5"}
	if acc, rej := CheckProposal(Proposal{Claims: []ProposedClaim{c}}, []byte(work)); len(acc) != 1 {
		t.Fatalf("trailing spaces and leading blank lines must not shift line numbers: %v", codes(rej))
	}
}

func TestCheckProposalDuplicatesAndTheCountCap(t *testing.T) {
	_, rej := CheckProposal(Proposal{Claims: []ProposedClaim{good(), good()}}, []byte(workText))
	if len(rej) != 1 || rej[0].Code != "duplicate" || rej[0].Index != 1 {
		t.Fatalf("%v", rej)
	}
	many := make([]ProposedClaim, MaxClaimsPerItem+3)
	for i := range many {
		many[i] = ProposedClaim{Text: "Jobs are acknowledged only after the handler returns." + strings.Repeat("!", i), Kind: "intent",
			Excerpt: "acknowledged only after the handler returns", Locator: "L3-L3"}
	}
	acc, rej := CheckProposal(Proposal{Claims: many}, []byte(workText))
	if len(acc) != MaxClaimsPerItem || len(rej) != 3 || rej[0].Code != "too-many" {
		t.Fatalf("accepted %d rejected %v", len(acc), codes(rej))
	}
}
