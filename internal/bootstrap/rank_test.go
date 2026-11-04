package bootstrap

import "testing"

func TestScoreWeightsAndCaps(t *testing.T) {
	base := Component{Prefix: "a"}
	if Score(base) != 0 {
		t.Fatal("no signals, no score")
	}
	if Score(Component{Churn: 5}) != 5*WeightChurn {
		t.Fatal("churn")
	}
	if Score(Component{Churn: 1000}) != float64(CapChurn)*WeightChurn {
		t.Fatal("churn is capped, so one noisy file cannot dominate")
	}
	if Score(Component{DocRefs: 2, HandoffMentions: 3, Entry: true}) != 2*WeightDocRef+3*WeightHandoff+WeightEntry {
		t.Fatal("combined")
	}
}

func TestRankOrdersByScoreThenNameAndMarksTheBudget(t *testing.T) {
	cs := []Component{
		{Name: "b", Prefix: "b", Churn: 10, Files: 1},
		{Name: "a", Prefix: "a", Churn: 10, Files: 1},
		{Name: "c", Prefix: "c", Churn: 40, Files: 1},
		{Name: "d", Prefix: "d", Files: 1},
	}
	got := Rank(cs, 2)
	order := []string{got[0].Name, got[1].Name, got[2].Name, got[3].Name}
	if order[0] != "c" || order[1] != "a" || order[2] != "b" || order[3] != "d" {
		t.Fatalf("order = %v (ties break by name)", order)
	}
	for i, c := range got {
		if c.Rank != i+1 || c.Inspected != (i < 2) {
			t.Fatalf("component %d: %+v", i, c)
		}
	}
	if cs[0].Rank != 0 {
		t.Fatal("Rank must not modify its input")
	}
}
