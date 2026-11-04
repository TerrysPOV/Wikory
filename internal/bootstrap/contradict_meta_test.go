package bootstrap

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/ids"
)

// A contradicted verdict marks the original claim conflicting. If the original's own evidence has
// gone stale since it was extracted, re-running the validator over it would quietly drop that
// evidence's object id. The original must keep its metadata as it was, apart from the status and
// the link to the claim that contradicts it.
func TestAContradictionChangesOnlyTheOriginalsStatusAndLink(t *testing.T) {
	v := newVerifyRig(t)
	before, err := claim.ParseComment(v.c1.Meta)
	if err != nil {
		t.Fatal(err)
	}
	env := envWith("internal/queue/queue.go", "internal/other/other.go")
	env.head["docs/decisions/0001.md"] = strings.Repeat("b", 40) // the doc moved on since extraction
	rep, err := ApplyVerdicts(context.Background(), VerifyInput{Runs: v.r, Env: env, Mint: ids.NewULID, Blob: blobOf40, Batch: v.batch,
		V: VerdictSet{Item: v.item.ID, Epoch: v.item.Epoch, Usage: Usage{InputTokens: 10, OutputTokens: 1},
			Verdicts: []Verdict{{Claim: v.c1.ClaimID, Result: "contradicted", Evidence: codeLoc(), Corrected: "The internal/queue package acknowledges before the handler returns."}}}})
	if err != nil || rep.Contradicted != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	got := claimByID(t, v.r, v.run.ID, v.c1.ClaimID)
	after, err := claim.ParseComment(got.Meta)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "conflicting" || len(after.Contradicts) != len(before.Contradicts)+1 {
		t.Fatalf("the original becomes conflicting and links the new claim: %+v", after)
	}
	if !reflect.DeepEqual(after.Evidence, before.Evidence) || !reflect.DeepEqual(after.Deps, before.Deps) || after.VRev != before.VRev || after.Kind != before.Kind {
		t.Fatalf("the original's metadata changed beyond status and link:\nbefore %+v\nafter  %+v", before, after)
	}
}
