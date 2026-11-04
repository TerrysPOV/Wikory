package bootstrap

import (
	"context"
	"testing"
)

// A file name confine refuses (a backslash, or a colon as the second byte) is recorded as a bad
// name and never queued: every claim drawn from it would be refused for confinement after the
// extraction was paid for, three times over.
func TestInventoryRecordsUnconfinableNamesAsBadNames(t *testing.T) {
	ctx := context.Background()
	repo := &memReader{name: "repo", files: map[string]string{
		"internal/a/a\\b.go": "package a\n",
		"c:x.md":             "notes\n",
		"internal/a/ok.go":   "package a\n",
	}}
	res, err := Inventory(ctx, []Reader{repo}, Options{MaxFileBytes: MaxFileBytes, Budget: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"internal/a/a\\b.go", "c:x.md"} {
		if got := srcBy(res, p); got.Disposition != DispBadName {
			t.Errorf("%q: disposition %q, want %q", p, got.Disposition, DispBadName)
		}
	}
	if got := srcBy(res, "internal/a/ok.go"); got.Disposition != DispQueued {
		t.Errorf("an ordinary name is queued: %+v", got)
	}
	for _, p := range repo.reads {
		if p != "internal/a/ok.go" {
			t.Errorf("%q was read", p)
		}
	}
}
