package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
)

// The notice for claims the index cannot read names them through safeLabel and only up to a cap:
// a claim id is chosen by a subagent, so it could carry a line break or run to any length.
func TestTheUnindexedNoticeIsSanitisedAndBounded(t *testing.T) {
	var ids []string
	for i := 0; i < 50; i++ {
		ids = append(ids, fmt.Sprintf("clm_%02d", i))
	}
	ids[0] = "clm_x\nforged notice line"
	got := strings.Join(finishNotices(bootstrap.Coverage{Unindexed: ids}), "\n")
	if strings.Contains(got, "\nforged") {
		t.Fatalf("a claim id must not break the notice into another line: %q", got)
	}
	if strings.Contains(got, "clm_49") || !strings.Contains(got, fmt.Sprintf("and %d more", 50-maxNoticeIDs)) {
		t.Fatalf("the notice names at most %d ids and counts the rest: %q", maxNoticeIDs, got)
	}
}
