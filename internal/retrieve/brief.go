package retrieve

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultHardCharCap stays well below Claude Code's 10,000-character cap on injected context.
const DefaultHardCharCap = 8000

// EstimateTokens is the binary's dependency-free token estimate.
func EstimateTokens(s string) int { return (utf8.RuneCountInString(s) + 3) / 4 }

// PendingTurn is a captured turn not yet consolidated.
type PendingTurn struct {
	At      time.Time
	TurnID  string
	State   string
	Request string
}

// OrientItem is one orientation line.
type OrientItem struct {
	Type    string
	Title   string
	OneLine string
	RelPath string
	Stale   bool
	// Evaluated is true when the item's claim was checked against the code (so Stale is an
	// answer); Unknown is true when it carries a claim that could not be checked. An item with
	// neither has no claim to check.
	Evaluated bool
	Unknown   bool
}

// BriefInput is everything the briefing shows.
type BriefInput struct {
	Mode             string
	LastConsolidated string
	Pending          []PendingTurn
	// PendingTotal is the true number of pending turns when Pending holds only the newest few.
	// Zero means Pending is complete.
	PendingTotal          int
	OtherCheckoutsPending int
	Failed                int
	Orientation           []OrientItem
	BudgetTokens          int
	HardCharCap           int
	Degraded              string
	Notices               []string
	IndexBuilding         bool
	// IndexPartial is RefreshStats.Partial: a size limit cut the index refresh short. The status
	// line says so, so it never implies the wiki is complete.
	IndexPartial bool
	// HookErrors is how many hook failures were logged in the last 24 hours; the status line
	// shows it when above zero.
	HookErrors int
}

var typeRank = map[string]int{"decision": 0, "failure-signature": 1, "component": 2}

func rank(t string) int {
	if r, ok := typeRank[t]; ok {
		return r
	}
	return 3
}

const closing = "</wikory-memory>\n"
const moreReserve = 40

// Brief renders the SessionStart briefing within the budget.
func Brief(in BriefInput) string {
	pending := append([]PendingTurn(nil), in.Pending...)
	sort.Slice(pending, func(i, j int) bool {
		if !pending[i].At.Equal(pending[j].At) {
			return pending[i].At.After(pending[j].At)
		}
		return pending[i].TurnID < pending[j].TurnID
	})
	orient := append([]OrientItem(nil), in.Orientation...)
	sort.Slice(orient, func(i, j int) bool {
		a, b := orient[i], orient[j]
		if rank(a.Type) != rank(b.Type) {
			return rank(a.Type) < rank(b.Type)
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		return a.RelPath < b.RelPath
	})
	stale, unknown, evaluated := 0, 0, 0
	for _, o := range orient {
		switch {
		case o.Unknown:
			unknown++
		case o.Stale:
			stale++
		}
		if o.Evaluated {
			evaluated++
		}
	}
	staleText := fmt.Sprintf("%d", stale)
	switch {
	case unknown > 0 && evaluated == 0:
		staleText = "unknown" // nothing was checked, so no count is honest
	case unknown > 0:
		staleText = fmt.Sprintf("%d, %d unknown", stale, unknown)
	}
	lastCons := in.LastConsolidated
	if lastCons == "" {
		lastCons = "never"
	}
	var b strings.Builder
	partial := ""
	if in.HookErrors > 0 {
		partial = fmt.Sprintf(" | hook errors %d", in.HookErrors)
	}
	if in.IndexPartial {
		partial += " | index partial"
	}
	pendingTotal := max(in.PendingTotal, len(pending))
	fmt.Fprintf(&b, "wikory: mode=%s | last consolidated: %s | pending %d | failed %d | stale %s%s. The wiki may not be current: verify against code.\n",
		oneLine(in.Mode, 40), oneLine(lastCons, 40), pendingTotal, in.Failed, staleText, partial)
	b.WriteString(Preface + "\n")
	if in.Degraded != "" {
		b.WriteString("wikory is degraded: " + Sanitize(in.Degraded) + "\n")
	}
	if in.IndexBuilding {
		b.WriteString("note: the index is still building; search may be incomplete.\n")
	}
	for _, n := range in.Notices {
		b.WriteString("note: " + Sanitize(n) + "\n")
	}
	b.WriteString("<wikory-memory>\n")
	limit := in.BudgetTokens * 4
	if in.HardCharCap > 0 && in.HardCharCap < limit {
		limit = in.HardCharCap
	}
	// Notices are not budgeted lines. If the header alone cannot fit, degrade to the
	// status-only form rather than exceed the budget or the hard cap.
	if b.Len()+len(closing)+moreReserve > limit {
		return StatusOnly(in)
	}
	fits := func(line string, cap int) bool {
		return b.Len()+len(line)+len(closing)+moreReserve <= cap
	}
	more := 0
	if len(pending) > 0 {
		pendingCap := min(b.Len()+limit/4, limit)
		header := "## Pending turns (this checkout)\n"
		if fits(header, pendingCap) {
			b.WriteString(header)
			for i, p := range pending {
				line := fmt.Sprintf("- %s [%s] %s\n", p.At.UTC().Format("2006-01-02 15:04"), p.State, oneLine(p.Request, 160))
				if !fits(line, pendingCap) {
					more += len(pending) - i
					break
				}
				b.WriteString(line)
			}
		} else {
			more += len(pending)
		}
	}
	more += pendingTotal - len(pending) // rows the query did not return are still pending
	if in.OtherCheckoutsPending > 0 {
		line := fmt.Sprintf("(%d pending in other checkouts)\n", in.OtherCheckoutsPending)
		if fits(line, limit) {
			b.WriteString(line)
		}
	}
	if len(orient) > 0 {
		header := "## Orientation\n"
		if fits(header, limit) {
			b.WriteString(header)
			for i, o := range orient {
				line := fmt.Sprintf("- %s: %s: %s", o.Type, oneLine(o.Title, 80), oneLine(o.OneLine, 140))
				if o.Stale {
					line += " [STALE]"
				}
				line += "\n"
				if !fits(line, limit) {
					more += len(orient) - i
					break
				}
				b.WriteString(line)
			}
		} else {
			more += len(orient)
		}
	}
	pointers := "## Pointers\n- wikory search <terms> to look deeper; read the cited page before relying on a claim.\n"
	if fits(pointers, limit) {
		b.WriteString(pointers)
	}
	if more > 0 {
		fmt.Fprintf(&b, "%d more available.\n", more)
	}
	b.WriteString(closing)
	return b.String()
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(Sanitize(s)), " ")
	return truncateRunes(s, n)
}

// StatusOnly is the minimal briefing: one sanitised line, no I/O, no counts it did not
// compute, and it still says when the index is partial. The SessionStart hook precomputes it
// before any discovery so that a timeout can return it unchanged (in.Degraded is the reason).
func StatusOnly(in BriefInput) string {
	reason := oneLine(in.Degraded, 120)
	if reason == "" {
		reason = "budget exceeded"
	}
	partial := ""
	if in.IndexPartial {
		partial = " | index partial"
	}
	out := fmt.Sprintf("wikory: mode=%s | briefing unavailable (%s)%s. The wiki may not be current: verify against code.\n",
		oneLine(in.Mode, 40), reason, partial)
	if in.HardCharCap > 0 && len(out) > in.HardCharCap {
		out = "wikory: briefing unavailable. The wiki may not be current: verify against code.\n"
	}
	return out
}
