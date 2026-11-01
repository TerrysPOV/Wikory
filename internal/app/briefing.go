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

// servable is the one gate for private data: a store exists, and this checkout is
// registered and trusted (spec 4.3, 10.4; spec question Q9).
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

// buildBriefing assembles the briefing for a servable checkout. A partial index is disclosed in
// the status line and the index's own notices are carried into the briefing.
func buildBriefing(ctx context.Context, p *project, mode string, deadline time.Time) (string, error) {
	stats, err := p.refresh(ctx, nil)
	if err != nil {
		return "", err
	}
	notices := append(append([]string(nil), p.Notices...), stats.Notices...)
	in := retrieve.BriefInput{Mode: mode, LastConsolidated: "never", BudgetTokens: p.Config.Retrieval.BudgetTokens,
		HardCharCap: retrieve.DefaultHardCharCap, Notices: notices, IndexPartial: stats.Partial}
	rows, err := p.Store.DB.QueryContext(ctx, `SELECT turn_id, state, request, created_at FROM turns WHERE checkout_id = ? ORDER BY created_at DESC LIMIT 50`, p.Checkout.ID)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var pt retrieve.PendingTurn
		var at string
		if err := rows.Scan(&pt.TurnID, &pt.State, &pt.Request, &at); err != nil {
			rows.Close()
			return "", err
		}
		pt.At, _ = time.Parse(time.RFC3339Nano, at)
		in.Pending = append(in.Pending, pt)
	}
	rows.Close()
	if err := p.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM turns WHERE checkout_id <> ?`, p.Checkout.ID).Scan(&in.OtherCheckoutsPending); err != nil {
		return "", err
	}
	if err := p.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE checkout_id = ? AND state IN ('conflict','refused')`, p.Checkout.ID).Scan(&in.Failed); err != nil {
		return "", err
	}
	orows, err := p.Store.DB.QueryContext(ctx, `
		SELECT p.relpath, p.type, p.title, COALESCE(c.text, ''), COALESCE(c.meta, '')
		FROM pages p LEFT JOIN claims c ON c.checkout_id = p.checkout_id AND c.relpath = p.relpath
		  AND c.ordinal = (SELECT min(ordinal) FROM claims WHERE checkout_id = p.checkout_id AND relpath = p.relpath)
		WHERE p.checkout_id = ? AND p.type IN ('decision','failure-signature','component')
		ORDER BY CASE p.type WHEN 'decision' THEN 0 WHEN 'failure-signature' THEN 1 ELSE 2 END, p.title LIMIT 30`, p.Checkout.ID)
	if err != nil {
		return "", err
	}
	var items []orientRow
	for orows.Next() {
		var o orientRow
		if err := orows.Scan(&o.rel, &o.typ, &o.title, &o.text, &o.meta); err != nil {
			orows.Close()
			return "", err
		}
		items = append(items, o)
	}
	orows.Close()
	env := p.validateEnv()
	for _, o := range items {
		item := retrieve.OrientItem{Type: o.typ, Title: o.title, OneLine: o.text, RelPath: o.rel}
		if o.meta != "" && time.Now().Before(deadline) {
			if c, err := claim.ParseComment(o.meta); err == nil {
				if d, err := validate.Derive(ctx, c, o.text, env); err == nil {
					item.Stale = d.Stale
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
