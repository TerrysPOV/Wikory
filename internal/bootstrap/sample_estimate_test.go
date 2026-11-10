package bootstrap

import "testing"

// The sample's cost is shown apart from the full run's: its sampled extract items at their own
// sizes, and one verify item per sampled item, up to the plan's verify item count. The sample
// size is not in the plan hash, so neither is this figure.
func TestSampleEstimatesCoverTheSampledExtractItemsAndTheirVerification(t *testing.T) {
	res := sampleResult()
	specs := ExtractSpecs(res) // the doc (4000 bytes), the README (8000), the queue digest
	one := SampleEstimates(res, 1)
	if len(one) != 2 || one[0].Stage != "extract" || one[1].Stage != "verify" {
		t.Fatalf("%+v", one)
	}
	if one[0].Items != 1 || one[0].InputTokens != int(specs[0].Bytes)/bytesPerToken+promptOverheadTokens || one[0].OutputTokens != extractOutPerItem {
		t.Fatalf("a sample of 1 is the first extract item at its own size: %+v", one[0])
	}
	if one[1] != verifyEstimate(1) {
		t.Fatalf("a sample of 1 verifies at most one component: %+v", one[1])
	}
	// Three sampled items, but the plan has one component to verify: the verify figure is capped.
	all := SampleEstimates(res, 3)
	if all[0] != EstimateStages(res)[0] || all[1] != verifyEstimate(1) {
		t.Fatalf("a sample of every item costs the full extraction and the plan's verification: %+v", all)
	}
	if SampleEstimates(res, 0) != nil {
		t.Fatal("no sample, no sample figure")
	}
	if BuildPlan(res, "repo", "h", 0).Hash != BuildPlan(res, "repo", "h", 3).Hash {
		t.Fatal("the sample size, and so the sample's figure, is not in the plan hash")
	}
}
