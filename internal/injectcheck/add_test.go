package injectcheck

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Fatalf("Add(2, 3) = %d, want 5", got)
	}
}

func TestDiv(t *testing.T) {
	if got := Div(6, 3); got != 2 {
		t.Fatalf("Div(6, 3) = %d, want 2", got)
	}
}
