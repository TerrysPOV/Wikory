package bootstrap

import "sort"

// Initial weights. They are constants recorded here and set properly from the sample run
// (architecture spec 14.2); changing one changes the plan hash, so it is a visible event.
const (
	WeightChurn   = 1.0
	WeightDocRef  = 3.0
	WeightHandoff = 2.0
	WeightEntry   = 20.0

	CapChurn   = 50
	CapDocRefs = 10
	CapHandoff = 10

	// InspectBudget is how many components get a verification pass by default.
	InspectBudget = 12
)

func capAt(n, limit int) int {
	if n > limit {
		return limit
	}
	return n
}

// Score is the weighted sum of a component's signals, each capped so one loud signal cannot
// decide the order alone.
func Score(c Component) float64 {
	s := float64(capAt(c.Churn, CapChurn))*WeightChurn +
		float64(capAt(c.DocRefs, CapDocRefs))*WeightDocRef +
		float64(capAt(c.HandoffMentions, CapHandoff))*WeightHandoff
	if c.Entry {
		s += WeightEntry
	}
	return s
}

// Rank scores the components, orders them by score (ties by name), numbers them from 1 and
// marks the first budget as inspected. It returns a new slice and leaves its input alone.
func Rank(cs []Component, budget int) []Component {
	out := append([]Component(nil), cs...)
	for i := range out {
		out[i].Score = Score(out[i])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	for i := range out {
		out[i].Rank = i + 1
		out[i].Inspected = i < budget
	}
	return out
}
