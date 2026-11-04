package bootstrap

import (
	"fmt"

	"github.com/TerrysPOV/Wikory/internal/gitx"
)

// UnreadSources turns the repository's other branches and worktrees into source records that
// say they were not read. A bootstrap baselines the default branch at HEAD only, so work that
// lives somewhere else is not in the baseline; listing it keeps the plan and the coverage
// report from implying otherwise. Worktrees are brought in by refresh after a merge, not by
// bootstrap (architecture spec section 9). The records carry no content and no hash.
func UnreadSources(def string, branches []gitx.Branch, trees []gitx.Worktree) []Source {
	var out []Source
	for _, b := range branches {
		out = append(out, Source{Name: "repo", Path: fmt.Sprintf("branch:%s (%d commit(s) not on %s)", b.Name, b.Ahead, def),
			Tier: TierAttributed, Disposition: DispUnmergedBranch, Date: dateOf(b.Tip)})
	}
	for _, w := range trees {
		on := w.Branch
		if on == "" {
			head := w.Head
			if len(head) > 12 {
				head = head[:12]
			}
			on = "detached at " + head
		}
		out = append(out, Source{Name: "repo", Path: fmt.Sprintf("worktree:%s (%s)", w.Path, on), Tier: TierAttributed, Disposition: DispOtherWorktree})
	}
	return out
}
