package validate

import (
	"context"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/claim"
)

// A claim that depends on two files but holds blob evidence for only one cannot say the other
// is unchanged. The unchecked file is named, so "sources unchanged" never implies it was seen.
func TestFreshnessNamesDependenciesItCouldNotCheck(t *testing.T) {
	c := stamped(claim.Claim{Kind: "current-impl", Status: "verified", VRev: "c0ffee0",
		Evidence: []claim.Evidence{blob("src/a.go", oidA)}, Deps: []string{"src/a.go#One", "src/b.go#Two"}})
	env := fakeEnv{head: map[string]string{"src/a.go": oidA}, work: map[string]string{"src/a.go": oidA, "src/b.go": oidB}}
	d, err := Derive(context.Background(), c, text, env)
	if err != nil {
		t.Fatal(err)
	}
	if d.Stale || !strings.Contains(d.Freshness, "src/b.go") || strings.HasSuffix(d.Freshness, "sources unchanged") {
		t.Fatalf("partial coverage was reported as fully checked: %+v", d)
	}
	// A changed checked file still wins over the disclosure.
	env.work["src/a.go"] = oidB
	d, _ = Derive(context.Background(), c, text, env)
	if !d.Stale || !strings.HasPrefix(d.Freshness, "STALE: src/a.go") {
		t.Fatalf("a changed dependency was not reported stale: %+v", d)
	}
}
