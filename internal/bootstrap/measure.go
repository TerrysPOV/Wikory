package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

// Verdicts counts a sample's verify verdicts as they were applied: a confirmation the validator
// would not accept is counted as not determinable, as the submit report counts it.
type Verdicts struct {
	Confirmed    int `json:"confirmed"`
	Contradicted int `json:"contradicted"`
	Undetermined int `json:"not_determinable"`
}

// Measure is what the sample showed, from the stored run. Tokens are asserted by the driver. The
// sample runs the paid pipeline on its items: extraction of the sampled extract items, then
// verification of the claims they yielded. Nothing is published.
type Measure struct {
	SampleItems int `json:"sample_items"` // sampled extract items
	SampleDone  int `json:"sample_done"`
	VerifyItems int `json:"verify_items"` // the sample's verify items
	VerifyDone  int `json:"verify_done"`
	// DurationSecs runs from the first lease to the last completion of any sampled item.
	DurationSecs float64 `json:"duration_secs"`
	// TokensIn and TokensOut are the whole sample's, extraction and verification together.
	TokensIn  int `json:"tokens_in"`
	TokensOut int `json:"tokens_out"`
	// ExtractTokens and VerifyTokens split the sample's tokens (input plus output) by stage.
	ExtractTokens int `json:"extract_tokens"`
	VerifyTokens  int `json:"verify_tokens"`
	// AcceptanceRate is the extract excerpt acceptance: accepted / (accepted + rejected); 0 when
	// nothing was proposed.
	AcceptanceRate float64  `json:"acceptance_rate"`
	Verdicts       Verdicts `json:"verdicts"`
	// ProjectedTokens is the extraction of the extract items the sample did not run, scaled from
	// the sample's extract items; asserted. It is nil (null in JSON) when it is unknown: extract
	// items remain but no sampled extract item completed. A zero is never shown for an unknown.
	ProjectedTokens *int `json:"projected_tokens"`
	// ProjectedVerifyTokens is the full run's verification: the sample's cost per verify item
	// times the plan's verify item count (inspected components with files); asserted. It is nil
	// when it is unknown: the plan has verify work but no sampled verify item completed.
	ProjectedVerifyTokens *int `json:"projected_verify_tokens"`
}

// ProjectionUnknown is how an unknown extraction projection is described to a person.
const ProjectionUnknown = "unknown: no sampled item completed"

// VerifyProjectionUnknown is how an unknown verification projection is described to a person.
const VerifyProjectionUnknown = "unknown: no sampled verify item completed"

// Measure reports a run's sample. A run planned without a sample reports zeros and no
// projection. Tokens and the acceptance rate are the sample's own: live while it runs, frozen
// when it settles, so the full run cannot move them; the per-stage split and the verdicts come
// from the sampled items, which are final once the sample settles. Everything stored is read in
// one snapshot, so a sample that settles mid-call cannot show live figures beside a settled state.
func (r *Runs) Measure(ctx context.Context, id string) (Measure, error) {
	var m Measure
	// The plan's components and sources never change after planning, so they are read before the
	// snapshot: the store has one connection, and a read beside an open transaction would wait.
	comps, err := r.Components(ctx, id)
	if err != nil {
		return m, err
	}
	srcs, err := r.Sources(ctx, id)
	if err != nil {
		return m, err
	}
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	var state string
	var sample, tin, tout, acc, rej, sin, sout, sacc, srej int
	err = tx.QueryRowContext(ctx, `SELECT state,sample,tokens_in,tokens_out,accepted,rejected,sample_in,sample_out,sample_acc,sample_rej
		FROM bs_runs WHERE run_id=? AND checkout_id=?`, id, r.CheckoutID).Scan(&state, &sample, &tin, &tout, &acc, &rej, &sin, &sout, &sacc, &srej)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNoRun
	}
	if err != nil || sample == 0 {
		return m, err
	}
	if err := m.readItems(ctx, tx, id, r.CheckoutID); err != nil {
		return m, err
	}
	if state != StateSampling {
		tin, tout, acc, rej = sin, sout, sacc, srej
	}
	m.TokensIn, m.TokensOut = tin, tout
	if n := acc + rej; n > 0 {
		m.AcceptanceRate = float64(acc) / float64(n)
	}
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bs_items WHERE run_id=? AND checkout_id=? AND stage='extract'`, id, r.CheckoutID).Scan(&total); err != nil {
		return m, err
	}
	m.ProjectedTokens = project(m.ExtractTokens, m.SampleDone, total-m.SampleItems)
	m.ProjectedVerifyTokens = project(m.VerifyTokens, m.VerifyDone, verifyEstimateItems(Result{Sources: srcs, Components: comps}))
	return m, nil
}

// readItems fills the per-stage counts, tokens, verdicts and duration from the sampled items.
func (m *Measure) readItems(ctx context.Context, tx *sql.Tx, runID, checkout string) error {
	rows, err := tx.QueryContext(ctx, `SELECT stage, count(*), coalesce(sum(CASE WHEN state='done' THEN 1 ELSE 0 END),0),
		coalesce(sum(tokens_in+tokens_out),0), coalesce(sum(confirmed),0), coalesce(sum(contradicted),0), coalesce(sum(undetermined),0)
		FROM bs_items WHERE run_id=? AND checkout_id=? AND sampled=1 GROUP BY stage`, runID, checkout)
	if err != nil {
		return err
	}
	for rows.Next() {
		var stage string
		var n, done, tokens int
		var v Verdicts
		if err := rows.Scan(&stage, &n, &done, &tokens, &v.Confirmed, &v.Contradicted, &v.Undetermined); err != nil {
			rows.Close()
			return err
		}
		if stage == "verify" {
			m.VerifyItems, m.VerifyDone, m.VerifyTokens, m.Verdicts = n, done, tokens, v
		} else {
			m.SampleItems, m.SampleDone, m.ExtractTokens = n, done, tokens
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var first, last sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT min(NULLIF(leased_at,'')), max(NULLIF(done_at,'')) FROM bs_items WHERE run_id=? AND checkout_id=? AND sampled=1`,
		runID, checkout).Scan(&first, &last); err != nil {
		return err
	}
	if first.Valid && last.Valid {
		a, e1 := time.Parse(tsLayout, first.String)
		b, e2 := time.Parse(tsLayout, last.String)
		if e1 == nil && e2 == nil {
			m.DurationSecs = b.Sub(a).Seconds()
		}
	}
	return nil
}

// project scales what done items cost to left more items. It is 0 when nothing is left, nil
// when something is left but no item completed, and clamped (never wrapped) where int is 32 bits.
func project(tokens, done, left int) *int {
	switch {
	case left <= 0:
		zero := 0
		return &zero
	case done == 0:
		return nil
	}
	p := int64(tokens) / int64(done) * int64(left)
	if p > math.MaxInt {
		p = math.MaxInt
	}
	n := int(p)
	return &n
}
