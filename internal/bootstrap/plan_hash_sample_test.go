package bootstrap

import "testing"

// Terry's ruling (2026-11-10, fix wave 3): the sample size is in the plan hash (version 4). The
// sample is paid work, extract and verify, so a yes to a plan is a yes to its sample size too.
func TestThePlanHashCoversTheSampleSize(t *testing.T) {
	res := sampleResult()
	seen := map[string]int{}
	for _, n := range []int{0, 1, 2, 3, 50} {
		h := BuildPlan(res, "repo", "abc123", n).Hash
		if prev, ok := seen[h]; ok {
			t.Fatalf("samples of %d and %d share a plan hash", prev, n)
		}
		seen[h] = n
		if again := BuildPlan(res, "repo", "abc123", n).Hash; again != h {
			t.Fatalf("the same plan and sample size must hash the same: %d", n)
		}
	}
}
