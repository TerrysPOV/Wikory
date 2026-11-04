package gitx

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// A file name may begin with a newline. Churn must count it under that exact name, not under the
// name with the newline stripped, which is another file (or none).
func TestChurnKeepsALeadingNewlineInAFileName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file names cannot hold a newline")
	}
	ctx := context.Background()
	dir := newRepo(t)
	commitFile(t, dir, "\nfoo.go", "x\n")
	commitFile(t, dir, "foo.go", "y\n")
	commitFile(t, dir, "foo.go", "z\n")
	r, err := Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Churn(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got["\nfoo.go"] != 1 || got["foo.go"] != 2 {
		t.Fatalf("churn = %v; want 1 for \"\\nfoo.go\" and 2 for \"foo.go\"", got)
	}
}
