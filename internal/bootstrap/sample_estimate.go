package bootstrap

// SampleEstimates sizes a sample of n extract items, apart from the full run: the sampled extract
// items (the same even spread Runs.Create marks) at their own sizes, and one verify item per
// sampled item, capped at the plan's verify item count. A sampled document's claims can name
// more than one component, so the verify figure is an estimate like the rest. It is nil when
// there is no sample. It is derived from the plan and the sample size, so it is fixed by the
// plan hash (planHash, version 4, hashes the sample size).
func SampleEstimates(res Result, n int) []Estimate {
	if n <= 0 {
		return nil
	}
	specs := ExtractSpecs(res)
	ex := Estimate{Stage: "extract"}
	for i := range SampleSet(n, len(specs)) {
		ex.Items++
		ex.InputTokens += int(specs[i].Bytes)/bytesPerToken + promptOverheadTokens
		ex.OutputTokens += extractOutPerItem
	}
	return []Estimate{ex, verifyEstimate(min(ex.Items, verifyEstimateItems(res)))}
}
