package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/redact"
)

// TokensNote is the label every report of token counts carries: status and the coverage report
// print whole-run totals the driver asserted, and the binary measures nothing itself.
const TokensNote = "token counts are whole-run totals asserted by the driver; the binary cannot measure them"

// PageOutcome is what happened to one page at finish.
type PageOutcome struct {
	Path    string `json:"path"`
	Outcome string `json:"outcome"` // written, unchanged or conflict
}

// notReadReasons are the dispositions of things that were never opened, as opposed to files that
// were read and then set aside (a secret, a binary). The report names each one.
var notReadReasons = map[string]bool{DispTooDeep: true, DispUnreadable: true, DispLink: true, DispUnmergedBranch: true, DispOtherWorktree: true}

// NotRead names one directory, file, branch or worktree the run never opened, and why.
type NotRead struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Date   string `json:"date,omitempty"`
}

// SecretSkip names a skipped file and the lines a secret was on. It never holds the secret, and
// when the file's own name is secret-shaped the name is withheld too (see safeLabel).
type SecretSkip struct {
	Path  string `json:"path"`
	Lines []int  `json:"lines"`
}

// Coverage is the report finish prints. It is built, outcomes included, before the run is marked
// done and stored with the run in the same transaction as that move (Runs.Finish), so status and
// a later finish reprint exactly what the first finish reported (Runs.StoredCoverage).
type Coverage struct {
	RunID   string `json:"run_id"`
	Head    string `json:"head"`
	Scope   string `json:"scope"`
	Sources struct {
		Read    int            `json:"read"`
		Skipped map[string]int `json:"skipped"`
		// Oldest and Newest are the source dates of the files that were read.
		Oldest string `json:"oldest"`
		Newest string `json:"newest"`
		// NotExtracted counts implementation files that were scanned but sit in components that
		// were not checked, so no claim was drawn from them.
		NotExtracted int `json:"not_extracted"`
	} `json:"sources"`
	SecretSkips []SecretSkip `json:"secret_skips"`
	NotRead     []NotRead    `json:"not_read"`
	Components  struct {
		Total        int      `json:"total"`
		Inspected    int      `json:"inspected"`
		Verified     []string `json:"verified"`
		Conflicting  []string `json:"conflicting"`
		Unknown      []string `json:"unknown"`
		NotInspected []string `json:"not_inspected"`
	} `json:"components"`
	Claims map[string]int `json:"claims"`
	// ClaimsByKind is kind -> status -> count.
	ClaimsByKind map[string]map[string]int `json:"claims_by_kind"`
	Proposals    struct {
		Accepted int `json:"accepted"`
		Rejected int `json:"rejected"`
		// Basis says what the two counts cover: extract submissions only.
		Basis string `json:"basis"`
	} `json:"proposals"`
	TokensIn     int           `json:"tokens_in"`
	TokensOut    int           `json:"tokens_out"`
	TokensNote   string        `json:"tokens_note"`
	DurationSecs float64       `json:"duration_s"`
	Pages        []PageOutcome `json:"pages"`
	Retirement   []string      `json:"retirement"`
	EndMarker    string        `json:"end_marker"`
	// StalePages are pages an earlier bootstrap wrote that this run did not render (a page that
	// shrank, a component that dropped out). They are still in the repository and may be out of
	// date; Wikory never deletes them.
	StalePages []string `json:"stale_pages"`
	// GivenUp counts items per stage that were tried MaxAttempts times and dropped: the source
	// or component behind each contributes no claims (extract) or stays unchecked (verify).
	GivenUp map[string]int `json:"items_given_up"`
	// Unindexed are claims a page holds that the indexer's parser cannot read even alone.
	Unindexed []string `json:"unindexed_claims"`
}

// safeLabel makes a name taken from the project (a file, a branch, a directory) fit to print. A
// name that is itself secret-shaped is withheld and shown only as a hash prefix. Otherwise each
// control or invisible character, and each line or paragraph separator, shows as "?", so a name
// cannot forge a line of the report, and the length is bounded.
func safeLabel(p string) string {
	if len(redact.Scan(p)) > 0 {
		return "(name withheld: it looks like a secret; hash " + ids.SHA256Hex([]byte(p))[:12] + ")"
	}
	var b strings.Builder
	n := 0
	for _, r := range p {
		if n++; n > maxShownRunes {
			b.WriteString("...")
			break
		}
		if unprintable(r) {
			r = '?'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// componentPageRe matches the file name of a component page: the readable slug, the six-digit
// hash of the prefix, and for a later part of a split page its number.
var componentPageRe = regexp.MustCompile(`^component-.*-([0-9a-f]{6})(?:-[0-9]+)?\.md$`)

// PageLabel names a rendered page for a message that must not repeat what the page was built
// from. A fixed page (overview, architecture and the like) is named by its path. A component
// page's name carries a slug of the component's directory, which comes from the project and may
// hold anything, so it is named only by the hash suffix that tells two component pages apart.
// A name that is neither is not printed.
func PageLabel(path string) string {
	base := path[strings.LastIndex(path, "/")+1:]
	if strings.HasPrefix(base, "component-") {
		if m := componentPageRe.FindStringSubmatch(base); m != nil {
			return "a component page (name ends -" + m[1] + ")"
		}
		return "a component page"
	}
	name, isMD := strings.CutSuffix(base, ".md")
	for _, fixed := range fixedPages {
		if rest, ok := strings.CutPrefix(name, fixed); isMD && ok && (rest == "" || (rest[0] == '-' && isPartSuffix(rest[1:]))) {
			return WikiDir + "/" + base
		}
	}
	return "a page with an unexpected name"
}

// fixedPages are the base names of the pages every bootstrap writes, whatever the project holds.
var fixedPages = []string{"overview", "architecture", "constraints", "known-issues", "unfinished-work"}

// isPartSuffix reports whether s is the number of a later part of a split page.
func isPartSuffix(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// PublishedPaths lists the pages an earlier bootstrap recorded as its own in this checkout.
func (r *Runs) PublishedPaths(ctx context.Context) ([]string, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT path FROM bs_pages WHERE checkout_id=? AND hash != '' ORDER BY path`, r.CheckoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// BuildCoverage assembles the report from the stored run. It reads no file text.
func BuildCoverage(ctx context.Context, runs *Runs, runID string, pages []PageOutcome, now time.Time) (Coverage, error) {
	var cov Coverage
	run, err := runs.Get(ctx, runID)
	if err != nil {
		return cov, err
	}
	srcs, err := runs.Sources(ctx, runID)
	if err != nil {
		return cov, err
	}
	comps, err := runs.Components(ctx, runID)
	if err != nil {
		return cov, err
	}
	claims, err := runs.Claims(ctx, runID)
	if err != nil {
		return cov, err
	}
	roots, err := runs.Roots(ctx, runID)
	if err != nil {
		return cov, err
	}
	cov.RunID, cov.Head, cov.Scope, cov.EndMarker = run.ID, run.Head, run.Scope, run.EndMarker
	st, err := runs.Status(ctx, runID)
	if err != nil {
		return cov, err
	}
	cov.GivenUp = map[string]int{}
	for stage, states := range st.Items {
		if n := states["rejected"]; n > 0 {
			cov.GivenUp[stage] = n
		}
	}
	cov.Sources.Skipped = map[string]int{}
	cov.Claims = map[string]int{}
	cov.ClaimsByKind = map[string]map[string]int{}
	perSource := map[string][3]int{} // name -> read, skipped, never opened
	for _, s := range srcs {
		c := perSource[s.Name]
		if s.Disposition == DispQueued {
			cov.Sources.Read++
			c[0]++
			if s.Date != "" {
				if cov.Sources.Oldest == "" || s.Date < cov.Sources.Oldest {
					cov.Sources.Oldest = s.Date
				}
				if s.Date > cov.Sources.Newest {
					cov.Sources.Newest = s.Date
				}
			}
		} else {
			cov.Sources.Skipped[s.Disposition]++
			c[1]++
			if notReadReasons[s.Disposition] {
				c[2]++
				cov.NotRead = append(cov.NotRead, NotRead{Source: s.Name, Path: safeLabel(s.Path), Reason: s.Disposition, Date: s.Date})
			}
		}
		perSource[s.Name] = c
		if s.SecretSpans > 0 {
			cov.SecretSkips = append(cov.SecretSkips, SecretSkip{Path: safeLabel(s.Path), Lines: s.SecretLines})
		}
	}
	for _, c := range claims {
		cov.Claims[c.Status]++
		if cov.ClaimsByKind[c.Kind] == nil {
			cov.ClaimsByKind[c.Kind] = map[string]int{}
		}
		cov.ClaimsByKind[c.Kind][c.Status]++
	}
	inspected := map[string]bool{}
	for _, c := range comps {
		inspected[c.Prefix] = c.Inspected
	}
	for prefix, files := range ComponentFiles(Result{Sources: srcs, Components: comps}) {
		if !inspected[prefix] {
			cov.Sources.NotExtracted += len(files)
		}
	}
	cov.Components.Total = len(comps)
	owned := AssignClaims(comps, claims) // the same owner the verifier and the page use
	for _, c := range comps {
		if !c.Inspected {
			cov.Components.NotInspected = append(cov.Components.NotInspected, safeLabel(c.Prefix))
			continue
		}
		cov.Components.Inspected++
		verified, conflicting := false, false
		for _, cl := range owned[c.Prefix] {
			verified = verified || cl.Status == "verified"
			conflicting = conflicting || cl.Status == "conflicting"
		}
		switch {
		case conflicting:
			cov.Components.Conflicting = append(cov.Components.Conflicting, safeLabel(c.Prefix))
		case verified:
			cov.Components.Verified = append(cov.Components.Verified, safeLabel(c.Prefix))
		default:
			cov.Components.Unknown = append(cov.Components.Unknown, safeLabel(c.Prefix))
		}
	}
	cov.Proposals.Accepted, cov.Proposals.Rejected = run.Accepted, run.Rejected
	cov.Proposals.Basis = "extract submissions only: claims admitted or rejected when they were first proposed, not verify verdicts (see claims by status for the outcome of verification)"
	cov.TokensIn, cov.TokensOut = run.TokensIn, run.TokensOut
	cov.TokensNote = TokensNote
	if created, err := time.Parse(tsLayout, run.CreatedAt); err == nil {
		cov.DurationSecs = now.Sub(created).Seconds()
	}
	cov.Pages = make([]PageOutcome, len(pages))
	rendered := map[string]bool{}
	for i, pg := range pages {
		cov.Pages[i] = PageOutcome{Path: safeLabel(pg.Path), Outcome: pg.Outcome}
		rendered[pg.Path] = true
	}
	recorded, err := runs.PublishedPaths(ctx)
	if err != nil {
		return cov, err
	}
	for _, p := range recorded {
		if !rendered[p] {
			cov.StalePages = append(cov.StalePages, safeLabel(p))
		}
	}
	sort.Strings(cov.StalePages)
	var names []string
	for n := range perSource {
		if n != "repo" && n != UnreadSourceName {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		c := perSource[n]
		dir := roots[n]
		if dir == "" {
			dir = "(directory not recorded)"
		}
		dir = safeLabel(dir)
		note := ""
		if c[2] > 0 {
			note = fmt.Sprintf(" %d of the skipped were never opened (too deep, unreadable or links) and are listed under not read; look at them before you delete anything.", c[2])
		}
		cov.Retirement = append(cov.Retirement, fmt.Sprintf("Source %q (%s): %d files read, %d skipped.%s Wikory has changed nothing there. When you are satisfied with the baseline, back it up and delete it by hand; Wikory never deletes a source.", n, dir, c[0], c[1], note))
	}
	return cov, nil
}

// Text is the human form of the report.
func (c Coverage) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Bootstrap %s finished at %.12s (scope %s)\n", c.RunID, c.Head, c.Scope)
	fmt.Fprintf(&b, "Sources: %d read", c.Sources.Read)
	keys := make([]string, 0, len(c.Sources.Skipped))
	for k := range c.Sources.Skipped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, ", %d %s", c.Sources.Skipped[k], k)
	}
	b.WriteString("\n")
	if c.Sources.Oldest != "" {
		fmt.Fprintf(&b, "  source dates of the files read: oldest %s, newest %s\n", c.Sources.Oldest, c.Sources.Newest)
	}
	if c.Sources.NotExtracted > 0 {
		fmt.Fprintf(&b, "  %d implementation file(s) sit in components that were not checked: scanned but not summarised\n", c.Sources.NotExtracted)
	}
	for _, n := range c.NotRead {
		fmt.Fprintf(&b, "  not read (%s): %s %s\n", strings.TrimPrefix(n.Reason, "skipped:"), n.Source, n.Path)
	}
	for _, s := range c.SecretSkips {
		nums := make([]string, len(s.Lines))
		for i, l := range s.Lines {
			nums[i] = fmt.Sprint(l)
		}
		where := "line " + strings.Join(nums, ", ")
		if len(nums) == 0 {
			where = "in the file name"
		}
		fmt.Fprintf(&b, "  skipped for a secret: %s (%s)\n", s.Path, where)
	}
	if len(c.GivenUp) > 0 {
		stages := make([]string, 0, len(c.GivenUp))
		for k := range c.GivenUp {
			stages = append(stages, k)
		}
		sort.Strings(stages)
		parts := make([]string, len(stages))
		for i, k := range stages {
			parts[i] = fmt.Sprintf("%d %s", c.GivenUp[k], k)
		}
		fmt.Fprintf(&b, "Work given up after %d attempts: %s item(s). Their sources contribute no claims and their components stay unchecked.\n", MaxAttempts, strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, "Components: %d found, %d checked: %d verified, %d conflicting, %d unknown; %d not checked\n",
		c.Components.Total, c.Components.Inspected, len(c.Components.Verified), len(c.Components.Conflicting), len(c.Components.Unknown), len(c.Components.NotInspected))
	fmt.Fprintf(&b, "Claims: %v.\n", c.Claims)
	fmt.Fprintf(&b, "Extract admissions: %d accepted, %d rejected (extract stage only; verify verdicts are not counted here).\n", c.Proposals.Accepted, c.Proposals.Rejected)
	kinds := make([]string, 0, len(c.ClaimsByKind))
	for k := range c.ClaimsByKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		total, parts := 0, []string(nil)
		states := make([]string, 0, len(c.ClaimsByKind[k]))
		for st := range c.ClaimsByKind[k] {
			states = append(states, st)
		}
		sort.Strings(states)
		for _, st := range states {
			total += c.ClaimsByKind[k][st]
			parts = append(parts, fmt.Sprintf("%s %d", st, c.ClaimsByKind[k][st]))
		}
		fmt.Fprintf(&b, "  %s: %d (%s)\n", k, total, strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, "Tokens (asserted): %d in, %d out. Duration: %.0fs.\n", c.TokensIn, c.TokensOut, c.DurationSecs)
	for _, p := range c.Pages {
		fmt.Fprintf(&b, "  %-9s %s\n", p.Outcome, p.Path)
	}
	if len(c.StalePages) > 0 {
		b.WriteString("Pages an earlier bootstrap wrote that this run did not render (not deleted; they may be out of date, review or remove them by hand):\n")
		for _, p := range c.StalePages {
			fmt.Fprintf(&b, "  stale     %s\n", p)
		}
	}
	if len(c.Unindexed) > 0 {
		shown := make([]string, len(c.Unindexed))
		for i, id := range c.Unindexed {
			shown[i] = safeLabel(id)
		}
		fmt.Fprintf(&b, "%d claim(s) are on a page but the index cannot read them, so search will not find them: %s\n", len(c.Unindexed), strings.Join(shown, ", "))
	}
	for _, r := range c.Retirement {
		b.WriteString(r + "\n")
	}
	if c.EndMarker != "" {
		fmt.Fprintf(&b, "End marker: %s. Turn capture starts from here.\n", c.EndMarker)
	}
	return b.String()
}

// Finish records the end marker and the coverage report and marks the run done, all in one
// transaction, so a done run always has its report. It works only from running or finishing.
func (r *Runs) Finish(ctx context.Context, id, marker string, cov Coverage) error {
	report, err := json.Marshal(cov)
	if err != nil {
		return fmt.Errorf("bootstrap: the coverage report cannot be stored: %w", err)
	}
	return r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE bs_runs SET state=?, end_marker=?, coverage=?, updated_at=? WHERE run_id=? AND checkout_id=? AND state IN (?,?)`,
			StateDone, marker, string(report), r.ts(), id, r.CheckoutID, StateRunning, StateFinishing)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: only a running or finishing run can finish", ErrState)
		}
		return nil
	})
}

// StoredCoverage is the report stored with a done run, and whether there is one.
func (r *Runs) StoredCoverage(ctx context.Context, id string) (Coverage, bool, error) {
	var cov Coverage
	var report string
	err := r.DB.QueryRowContext(ctx, `SELECT coverage FROM bs_runs WHERE run_id=? AND checkout_id=?`, id, r.CheckoutID).Scan(&report)
	if errors.Is(err, sql.ErrNoRows) {
		return cov, false, ErrNoRun
	}
	if err != nil || report == "" {
		return cov, false, err
	}
	if err := json.Unmarshal([]byte(report), &cov); err != nil {
		return cov, false, fmt.Errorf("bootstrap: the stored coverage report does not parse: %w", err)
	}
	return cov, true, nil
}

// EnterFinishing moves a running run to finishing, from where nothing new can be handed out or
// committed. It reports whether this call made the move, so that a caller that has to back out
// undoes only its own move: a run that was already finishing (an earlier finish failed after the
// move) is left as it is. A run in any other state is refused.
func (r *Runs) EnterFinishing(ctx context.Context, id string) (bool, error) {
	moved := false
	err := r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		moved = false
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM bs_runs WHERE run_id=? AND checkout_id=?`, id, r.CheckoutID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoRun
		}
		if err != nil {
			return err
		}
		switch state {
		case StateFinishing:
			return nil
		case StateRunning:
			if _, err := tx.ExecContext(ctx, `UPDATE bs_runs SET state=?, updated_at=? WHERE run_id=? AND checkout_id=? AND state=?`,
				StateFinishing, r.ts(), id, r.CheckoutID, StateRunning); err != nil {
				return err
			}
			moved = true
			return nil
		default:
			return fmt.Errorf("%w: a %s run cannot move to finishing", ErrState, state)
		}
	})
	return moved && err == nil, err
}

// Abandon drops a run that has not finished: planned, sampling, awaiting-confirm, running or
// finishing become abandoned. Nothing is promoted or deleted: pages the run already published
// keep their pending hashes, so a later bootstrap still recognises them as Wikory's own. An
// abandoned run is never leased, confirmed, submitted to or finished.
func (r *Runs) Abandon(ctx context.Context, id string) error {
	return r.Transition(ctx, id, StateAbandoned, StatePlanned, StateSampling, StateAwaiting, StateRunning, StateFinishing)
}

// LeaveFinishing puts a finishing run back to running, so that its open work can be handed out
// and submitted again.
func (r *Runs) LeaveFinishing(ctx context.Context, id string) error {
	return r.Transition(ctx, id, StateRunning, StateFinishing)
}
