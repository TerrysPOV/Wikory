package bootstrap

import "testing"

// Claim text goes into a page unescaped, so link and image markup must be refused when the claim
// is proposed, with its own reject code and a fixed message.
func TestCheckTextRefusesLinkAndImageMarkup(t *testing.T) {
	for _, in := range []string{
		"The queue is documented [here](http://evil.example/x).",
		"See ![diagram](http://evil.example/p.png) for the flow.",
		"An image ![alt] reference is markup too.",
	} {
		code, why := CheckText(in)
		if code != "markup" || why != "the text contains link or image markup, which a page would render as a link or an image" {
			t.Errorf("CheckText(%q) = %q %q, want the markup rejection", in, code, why)
		}
	}
	for _, in := range []string{"The handler returns (after the ack).", "Arrays are indexed as a[i] (zero based)."} {
		if code, _ := CheckText(in); code != "" {
			t.Errorf("ordinary prose with brackets was refused: %q (%s)", in, code)
		}
	}
}
