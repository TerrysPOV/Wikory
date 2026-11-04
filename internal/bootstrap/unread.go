package bootstrap

import (
	"fmt"

	"github.com/TerrysPOV/Wikory/internal/gitx"
)

// UnreadSources turns the repository's other branches and worktrees into source records that
// say they were not read. A bootstrap baselines the default branch at HEAD only, so work that
// lives somewhere else is not in the baseline; listing it keeps the plan and the coverage
// report from implying otherwise. Worktrees are brought in by refresh after a merge, not by
// bootstrap (architecture spec section 9). The records carry no content and no hash, and no machine-specific path.
func UnreadSources(def string, branches []gitx.Branch, trees []gitx.Worktree) []Source {
	var out []Source
	for _, b := range branches {
		out = append(out, Source{Name: "repo", Path: fmt.Sprintf("branch:%s (%d commit(s) not on %s)", b.Name, b.Ahead, def),
			Tier: TierAttributed, Disposition: DispUnmergedBranch, Date: dateOf(b.Tip)})
	}
	for _, w := range trees {
		// The label is the branch (git lets one worktree hold a branch, so it is unique) or a
		// short head id when detached. The directory is left out on purpose: it differs by
		// machine and moves with the worktree, and the plan hash covers this path.
		label := w.Branch
		if label == "" {
			label = w.Head
			if len(label) > 12 {
				label = label[:12]
			}
		}
		out = append(out, Source{Name: "repo", Path: "worktree:" + label, Tier: TierAttributed, Disposition: DispOtherWorktree})
	}
	return out
}
