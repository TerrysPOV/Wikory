package app

import (
	"context"
	"strconv"
	"time"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/retrieve"
	"github.com/TerrysPOV/Wikory/internal/validate"
)

// servable is the gate for serving private data: a store exists, and this checkout is
// registered and trusted (spec 4.3, 10.4; plan adjudication Q9). Search, context, the hooks and
// the briefing all pass through it. `wikory recover` does not: it requires registration only,
// because it finishes publish jobs that Wikory itself began in this checkout and reads nothing
// out to the session.
func servable(p *project) bool { return p.Store != nil && p.Registered && p.Trusted }

func (p *project) validateEnv() validate.GitEnv {
	return validate.GitEnv{Repo: p.Repo, Root: p.RepoRoot, MaxBytes: 4 << 20}
}

func (p *project) refresh(ctx context.Context, prog *Progress) (retrieve.RefreshStats, error) {
	ix := &retrieve.Indexer{DB: p.Store.DB, CheckoutID: p.Checkout.ID, Repo: p.RepoRoot}
	if prog != nil {
		first := true
		ix.Progress = func(done, total int) {
			if first {
				first = false
				est := time.Duration(total/500) * time.Second
				prog.Begin("indexing", total, est, "Indexing "+strconv.Itoa(total)+" wiki pages (estimate, not yet measured). Safe to interrupt; it resumes.")
			}
			prog.Step(done)
		}
		defer prog.End()
	}
	return ix.Refresh(ctx)
}

type orientRow struct{ rel, typ, title, text, meta string }

// pendingTurns lists this checkout's 50 most recent turns, captured or sealed (nothing is
// consolidated in 1a, so every turn is still pending), and returns the true number of them: the
// LIMIT bounds what is rendered, never what is counted.
func (p *project) pendingTurns(ctx context.Context) ([]retrieve.PendingTurn, int, error) {
	var total int
	if err := p.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM turns WHERE checkout_id = ?`, p.Checkout.ID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := p.Store.DB.QueryContext(ctx, `SELECT turn_id, state, request, created_at FROM turns WHERE checkout_id = ? ORDER BY created_at DESC LIMIT 50`, p.Checkout.ID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var turns []retrieve.PendingTurn
	for rows.Next() {
		var pt retrieve.PendingTurn
		var at string
		if err := rows.Scan(&pt.TurnID, &pt.State, &pt.Request, &at); err != nil {
			return nil, 0, err
		}
		pt.At, _ = time.Parse(time.RFC3339Nano, at)
		turns = append(turns, pt)
	}
	return turns, total, rows.Err()
}

// stuckJobAge is how long a publish job may sit begun or applying before it counts as stuck.
// It equals the publisher's AbandonAfter (project.go), after which `wikory recover` abandons it.
const stuckJobAge = 10 * time.Minute

// stuckJobs counts this checkout's publish jobs that have been begun or applying for longer
// than stuckJobAge: a process stopped part way, and only `wikory recover` can finish or
// abandon them. A job begun a moment ago is in flight, not stuck.
func (p *project) stuckJobs(ctx context.Context, now time.Time) (int, error) {
	rows, err := p.Store.DB.QueryContext(ctx, `SELECT updated_at FROM jobs WHERE checkout_id = ? AND state IN ('begun','applying')`, p.Checkout.ID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var at string
		if err := rows.Scan(&at); err != nil {
			return 0, err
		}
		if t, err := time.Parse(time.RFC3339Nano, at); err != nil || now.Sub(t) >= stuckJobAge {
			n++ // an unreadable time cannot be shown to be recent
		}
	}
	return n, rows.Err()
}

// orientRows lists the first claim of each decision, failure-signature and component page.
func (p *project) orientRows(ctx context.Context) ([]orientRow, error) {
	rows, err := p.Store.DB.QueryContext(ctx, `
		SELECT p.relpath, p.type, p.title, COALESCE(c.text, ''), COALESCE(c.meta, '')
		FROM pages p LEFT JOIN claims c ON c.checkout_id = p.checkout_id AND c.relpath = p.relpath
		  AND c.ordinal = (SELECT min(ordinal) FROM claims WHERE checkout_id = p.checkout_id AND relpath = p.relpath)
		WHERE p.checkout_id = ? AND p.type IN ('decision','failure-signature','component')
		ORDER BY CASE p.type WHEN 'decision' THEN 0 WHEN 'failure-signature' THEN 1 ELSE 2 END, p.title LIMIT 30`, p.Checkout.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []orientRow
	for rows.Next() {
		var o orientRow
		if err := rows.Scan(&o.rel, &o.typ, &o.title, &o.text, &o.meta); err != nil {
			return nil, err
		}
		items = append(items, o)
	}
	return items, nil
}

// buildBriefing assembles the briefing for a servable checkout. A partial index is disclosed in
// the status line and the index's own notices are carried into the briefing.
func buildBriefing(ctx context.Context, p *project, env *Env, mode string, deadline time.Time) (string, error) {
	stats, err := p.refresh(ctx, nil)
	if err != nil {
		return "", err
	}
	notices := append(append([]string(nil), p.Notices...), stats.Notices...)
	in := retrieve.BriefInput{Mode: mode, LastConsolidated: "never", BudgetTokens: p.Config.Retrieval.BudgetTokens,
		HardCharCap: retrieve.DefaultHardCharCap, Notices: notices, IndexPartial: stats.Partial,
		HookErrors: readHookLog(env, time.Now()).Recent}
	if in.Pending, in.PendingTotal, err = p.pendingTurns(ctx); err != nil {
		return "", err
	}
	if err := p.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM turns WHERE checkout_id <> ?`, p.Checkout.ID).Scan(&in.OtherCheckoutsPending); err != nil {
		return "", err
	}
	if err := p.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE checkout_id = ? AND state IN ('conflict','refused')`, p.Checkout.ID).Scan(&in.Failed); err != nil {
		return "", err
	}
	stuck, err := p.stuckJobs(ctx, time.Now())
	if err != nil {
		return "", err
	}
	in.Failed += stuck // a job that never finished has failed as far as the reader is concerned
	items, err := p.orientRows(ctx)
	if err != nil {
		return "", err
	}
	venv := p.validateEnv()
	for _, o := range items {
		item := retrieve.OrientItem{Type: o.typ, Title: o.title, OneLine: o.text, RelPath: o.rel}
		if o.meta != "" {
			// A claim that is not checked (the deadline passed, or it did not parse or derive) is
			// unknown, never counted as not stale.
			item.Unknown = true
			if time.Now().Before(deadline) {
				if c, err := claim.ParseComment(o.meta); err == nil {
					if d, err := validate.Derive(ctx, c, o.text, venv); err == nil {
						item.Stale, item.Evaluated, item.Unknown = d.Stale, true, false
					}
				}
			}
		}
		in.Orientation = append(in.Orientation, item)
	}
	return retrieve.Brief(in), nil
}

// notRegisteredBriefing is all a checkout that is not servable gets.
func notRegisteredBriefing(p *project) string {
	why := "this checkout is not registered with this machine's Wikory store"
	if p.Store != nil && p.Registered && !p.Trusted {
		why = "this checkout is registered but not trusted"
	}
	return "wikory: " + why + ", so no memory is served. If this is your project, run wikory init here; " +
		"trust assessment for other projects arrives in a later version.\n" + retrieve.Preface + "\n"
}

// degradedBriefing is the fallback on timeout or an unreadable database (spec 7.4).
func degradedBriefing(root *confine.Root, reason string) string {
	out := "wikory: mode=degraded | pending unknown | failed unknown | stale unknown. The wiki may not be current: verify against code.\n" +
		retrieve.Preface + "\nwikory is degraded: " + retrieve.Sanitize(reason) + ". Run wikory doctor.\n"
	if root != nil {
		if data, err := root.ReadFile("project-memory/index.md", 64<<10); err == nil {
			idx := retrieve.Sanitize(string(data))
			if len(idx) > 2000 {
				idx = idx[:2000] + "..."
			}
			out += "<wikory-memory>\n" + idx + "\n</wikory-memory>\n"
		}
	}
	return out
}
