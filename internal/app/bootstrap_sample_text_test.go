package app

import (
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
)

// A sample in which no item completed has no projection: the status says so, and still shows
// the tokens the failed attempts spent. It must never print a projection of 0 tokens.
func TestSampleTextSaysAnUnknownProjectionIsUnknown(t *testing.T) {
	got := sampleText(bootstrap.Measure{SampleItems: 2, TokensIn: 1500, TokensOut: 150})
	if !strings.Contains(got, "projected extraction for the rest unknown: no sampled item completed") || strings.Contains(got, "about 0 tokens") {
		t.Fatalf("an unknown projection must be named as unknown: %s", got)
	}
	if !strings.Contains(got, "tokens (asserted) 1500 in 150 out") {
		t.Fatalf("the tokens already spent are still shown: %s", got)
	}
	n := 4200
	if got := sampleText(bootstrap.Measure{SampleItems: 2, SampleDone: 2, ProjectedTokens: &n}); !strings.Contains(got, "about 4200 tokens (asserted)") {
		t.Fatalf("a known projection is shown: %s", got)
	}
}
