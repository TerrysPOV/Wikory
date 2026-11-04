// Package bootstrap builds a labelled current-state baseline wiki from a project's code, docs,
// handoffs and memory notes. It does no model work: it inventories, redacts, ranks, hands out
// work, validates what comes back and renders pages. The model-driven stages run in subagents.
package bootstrap

import (
	"context"
	"time"
)

// Tiers say what a source can establish. Code, configuration and tests establish what the
// project does now; decision records establish intent; everything else is somebody's account.
const (
	TierImplementation = "implementation"
	TierIntent         = "intent"
	TierAttributed     = "attributed"
)

// Source dispositions.
const (
	DispQueued = "queued"
	DispRead   = "read"

	// Dispositions of things that were never opened. The coverage report lists each by name.
	DispTooDeep        = "skipped:too-deep"
	DispUnreadable     = "skipped:unreadable"
	DispLink           = "skipped:link"
	DispUnmergedBranch = "skipped:unmerged-branch"
	DispOtherWorktree  = "skipped:other-worktree"
)

// Entry is one listed file before it is read.
type Entry struct {
	Source string
	Path   string
	OID    string
	Size   int64
	// Modified is the file's modification time. Only directory readers know it; repository
	// files are dated from the commit log instead.
	Modified time.Time
}

// Reader lists and reads one source of material. Read never returns more than max bytes.
type Reader interface {
	Name() string
	Tier(path string) string
	List(ctx context.Context) ([]Entry, error)
	Read(ctx context.Context, e Entry, max int64) ([]byte, error)
}

// Source is one file's inventory record. It never holds file text or secret text.
type Source struct {
	Name, Path, OID, Tier, Hash, Disposition string
	Size                                     int64
	SecretSpans                              int
	SecretLines                              []int
	// Date is when the file last changed, RFC3339 in UTC: the last commit time for a repository
	// file, the modification time for a file read from a directory. It is for the report only
	// and is not part of the plan hash.
	Date string
}

// Skip is something a reader dropped without listing it, and why. Reason is a disposition such
// as DispTooDeep. Path ends in "/" when it names a directory that was not entered.
type Skip struct {
	Path   string
	Reason string
}

// Skipper is implemented by a reader that can drop entries. Inventory records every skip as a
// source with that disposition and no content, so the coverage report and the retirement
// checklist can name what was not read instead of losing it.
type Skipper interface{ Skipped() []Skip }

// Component is a unit of the project that gets its own page and, when ranked high enough, a
// verification pass.
type Component struct {
	Name, Prefix                           string
	Files, Churn, DocRefs, HandoffMentions int
	Entry                                  bool
	Score                                  float64
	Rank                                   int
	Inspected                              bool
}

// Result is what inventory produces.
type Result struct {
	Sources    []Source
	Components []Component
}
