package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/ids"
)

var (
	ErrNoRun      = errors.New("bootstrap: no such run in this checkout")
	ErrNoItem     = errors.New("bootstrap: no such item in this checkout")
	ErrStaleLease = errors.New("bootstrap: the lease on this item expired or was taken by a later batch")
	ErrState      = errors.New("bootstrap: the run is not in a state that allows this")
)

// Run states.
const (
	StatePlanned   = "planned"
	StateSampling  = "sampling"
	StateAwaiting  = "awaiting-confirm"
	StateRunning   = "running"
	StateFinishing = "finishing"
	StateDone      = "done"
	StateAbandoned = "abandoned"
	LeaseTTL       = 30 * time.Minute
	MaxAttempts    = 3
	maxBatch       = 50
	tsLayout       = "2006-01-02T15:04:05.000000000Z"
)

// Item is one leased or stored unit of model work.
type Item struct {
	ID, RunID, Stage, Ref, InputHash, State, Batch string
	Epoch, Attempts                                int
}

// Batch is the items handed out by one Next call.
type Batch struct {
	ID    string
	Items []Item
}

// Usage is what the driver says a submission cost. The binary cannot measure it, so it is
// recorded and reported as asserted.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Run is one bootstrap run.
type Run struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	PlanHash  string `json:"plan_hash"`
	Scope     string `json:"scope"`
	Head      string `json:"head"`
	EndMarker string `json:"end_marker"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Sample    int    `json:"sample"`
	TokensIn  int    `json:"tokens_in"`
	TokensOut int    `json:"tokens_out"`
	Accepted  int    `json:"claims_accepted"`
	Rejected  int    `json:"claims_rejected"`
}

// RunStatus is a run with its item and source counts.
type RunStatus struct {
	Run          Run
	Items        map[string]map[string]int
	Dispositions map[string]int
	Components   int
	Claims       map[string]int
}

// Runs stores bootstrap runs for one checkout. Every write runs in one transaction under the
// database write lock.
type Runs struct {
	DB         *sql.DB
	CheckoutID string
	Lock       func(context.Context, func(context.Context) error) error
	Now        func() time.Time
	NewID      func(prefix string) string
}

// NewRuns binds a run store to a checkout and the store's write lock.
func NewRuns(db *sql.DB, checkoutID string, lock func(context.Context, func(context.Context) error) error) *Runs {
	return &Runs{DB: db, CheckoutID: checkoutID, Lock: lock, Now: time.Now,
		NewID: func(prefix string) string { return prefix + ids.NewULID() }}
}

func (r *Runs) ts() string { return r.Now().UTC().Format(tsLayout) }

func (r *Runs) write(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error {
	return r.Lock(ctx, func(ctx context.Context) error {
		tx, err := r.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// Create stores a planned run: its sources, components and extract items. Items in the sample
// are flagged so a sampling run leases only those.
func (r *Runs) Create(ctx context.Context, p Plan, specs []ItemSpec) (Run, error) {
	now := r.ts()
	run := Run{ID: r.NewID("run_"), State: StatePlanned, PlanHash: p.Hash, Scope: p.Scope, Head: p.Head, Sample: p.Sample, CreatedAt: now, UpdatedAt: now}
	sampled := SampleSet(p.Sample, len(specs))
	err := r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO bs_runs(run_id,checkout_id,state,plan_hash,scope,head_commit,sample,roots,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, run.ID, r.CheckoutID, run.State, run.PlanHash, run.Scope, run.Head, run.Sample, encodeRoots(p.Roots), now, now); err != nil {
			return err
		}
		for _, s := range p.Sources {
			lines := make([]string, len(s.SecretLines))
			for i, l := range s.SecretLines {
				lines[i] = strconv.Itoa(l)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO bs_sources(run_id,checkout_id,source,path,oid,tier,hash,size,disposition,secret_spans,secret_lines,src_date)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, r.CheckoutID, s.Name, s.Path, s.OID, s.Tier, s.Hash, s.Size, s.Disposition, s.SecretSpans, strings.Join(lines, ","), s.Date); err != nil {
				return err
			}
		}
		for _, c := range p.Components {
			if _, err := tx.ExecContext(ctx, `INSERT INTO bs_components(run_id,checkout_id,name,prefix,score,rank,inspected) VALUES(?,?,?,?,?,?,?)`,
				run.ID, r.CheckoutID, c.Name, c.Prefix, c.Score, c.Rank, boolInt(c.Inspected)); err != nil {
				return err
			}
		}
		for i, sp := range specs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO bs_items(item_id,run_id,checkout_id,stage,ref,input_hash,state,sampled) VALUES(?,?,?,?,?,?,'pending',?)`,
				r.NewID("itm_"), run.ID, r.CheckoutID, sp.Stage, sp.Ref, sp.InputHash, boolInt(sampled[i])); err != nil {
				return err
			}
		}
		return nil
	})
	return run, err
}

// encodeRoots stores the --source directories as sorted name=dir lines.
func encodeRoots(m map[string]string) string {
	lines := make([]string, 0, len(m))
	for k, v := range m {
		lines = append(lines, k+"="+v)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// Roots returns the --source directories a run was planned with.
func (r *Runs) Roots(ctx context.Context, id string) (map[string]string, error) {
	var raw string
	err := r.DB.QueryRowContext(ctx, `SELECT roots FROM bs_runs WHERE run_id=? AND checkout_id=?`, id, r.CheckoutID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoRun
	}
	out := map[string]string{}
	for _, l := range strings.Split(raw, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			out[k] = v
		}
	}
	return out, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

const runCols = `run_id,state,plan_hash,scope,head_commit,sample,end_marker,tokens_in,tokens_out,accepted,rejected,created_at,updated_at`

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var x Run
	err := row.Scan(&x.ID, &x.State, &x.PlanHash, &x.Scope, &x.Head, &x.Sample, &x.EndMarker, &x.TokensIn, &x.TokensOut, &x.Accepted, &x.Rejected, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNoRun
	}
	return x, err
}

// Get returns a run of this checkout.
func (r *Runs) Get(ctx context.Context, id string) (Run, error) {
	return scanRun(r.DB.QueryRowContext(ctx, `SELECT `+runCols+` FROM bs_runs WHERE run_id=? AND checkout_id=?`, id, r.CheckoutID))
}

// Latest returns this checkout's most recently created run.
func (r *Runs) Latest(ctx context.Context) (Run, error) {
	return scanRun(r.DB.QueryRowContext(ctx, `SELECT `+runCols+` FROM bs_runs WHERE checkout_id=? ORDER BY created_at DESC, run_id DESC LIMIT 1`, r.CheckoutID))
}

// Transition moves a run to a new state, but only from one of the listed states.
func (r *Runs) Transition(ctx context.Context, id, to string, from ...string) error {
	if len(from) == 0 {
		return fmt.Errorf("%w: no source state given", ErrState)
	}
	return r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if to == StateSampling {
			var sample int
			err := tx.QueryRowContext(ctx, `SELECT sample FROM bs_runs WHERE run_id=? AND checkout_id=?`, id, r.CheckoutID).Scan(&sample)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNoRun
			}
			if err != nil {
				return err
			}
			if sample == 0 {
				return fmt.Errorf("%w: the run was planned without a sample, so there is nothing to measure; plan it with a sample size above 0", ErrState)
			}
		}
		args := []any{to, r.ts(), id, r.CheckoutID}
		for _, f := range from {
			args = append(args, f)
		}
		res, err := tx.ExecContext(ctx, `UPDATE bs_runs SET state=?, updated_at=? WHERE run_id=? AND checkout_id=? AND state IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(from)), ",")+`)`, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, gerr := scanRun(tx.QueryRowContext(ctx, `SELECT `+runCols+` FROM bs_runs WHERE run_id=? AND checkout_id=?`, id, r.CheckoutID)); gerr != nil {
				return gerr
			}
			return fmt.Errorf("%w: cannot move to %s from the run's current state", ErrState, to)
		}
		if to == StateSampling {
			return settleSample(ctx, tx, id, r.ts()) // an empty sample has nothing to run
		}
		return nil
	})
}

// Next leases up to max pending items. Expired leases are reclaimed first (an item that has
// expired MaxAttempts times is rejected). A sampling run leases only sampled extract items;
// verify items are leased only once no extract item is open. An empty batch means nothing is
// left to lease right now.
func (r *Runs) Next(ctx context.Context, id string, max int) (Batch, error) {
	if max <= 0 || max > maxBatch {
		max = BatchSize
	}
	var b Batch
	err := r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		run, err := scanRun(tx.QueryRowContext(ctx, `SELECT `+runCols+` FROM bs_runs WHERE run_id=? AND checkout_id=?`, id, r.CheckoutID))
		if err != nil {
			return err
		}
		if run.State != StateSampling && run.State != StateRunning {
			return fmt.Errorf("%w: the run is %s; work is handed out only while it is sampling or running (wikory bootstrap confirm)", ErrState, run.State)
		}
		restrict := boolInt(run.State == StateSampling)
		cutoff := r.Now().Add(-LeaseTTL).UTC().Format(tsLayout)
		if _, err := tx.ExecContext(ctx, `UPDATE bs_items SET attempts=attempts+1, state=CASE WHEN attempts+1 >= ? THEN 'rejected' ELSE 'pending' END
			WHERE run_id=? AND state='leased' AND leased_at < ?`, MaxAttempts, id, cutoff); err != nil {
			return err
		}
		var open int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bs_items WHERE run_id=? AND stage='extract' AND state IN ('pending','leased') AND (?=0 OR sampled=1)`,
			id, restrict).Scan(&open); err != nil {
			return err
		}
		stage := "extract"
		if open == 0 {
			if restrict == 1 {
				return settleSample(ctx, tx, id, r.ts()) // no sampled item left to lease
			}
			stage = "verify"
		}
		rows, err := tx.QueryContext(ctx, `SELECT item_id,ref,input_hash,epoch,attempts FROM bs_items
			WHERE run_id=? AND stage=? AND state='pending' AND (?=0 OR sampled=1) ORDER BY rowid LIMIT ?`, id, stage, restrict, max)
		if err != nil {
			return err
		}
		var picked []Item
		for rows.Next() {
			it := Item{RunID: id, Stage: stage, State: "leased"}
			if err := rows.Scan(&it.ID, &it.Ref, &it.InputHash, &it.Epoch, &it.Attempts); err != nil {
				rows.Close()
				return err
			}
			picked = append(picked, it)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(picked) == 0 {
			return nil
		}
		b.ID = r.NewID("bat_")
		now := r.ts()
		for i := range picked {
			picked[i].Epoch++
			picked[i].Batch = b.ID
			if _, err := tx.ExecContext(ctx, `UPDATE bs_items SET state='leased', epoch=?, batch=?, leased_at=? WHERE item_id=?`, picked[i].Epoch, b.ID, now, picked[i].ID); err != nil {
				return err
			}
		}
		b.Items = picked
		return nil
	})
	return b, err
}

func loadItem(ctx context.Context, tx *sql.Tx, id, checkout string) (Item, error) {
	it := Item{ID: id}
	err := tx.QueryRowContext(ctx, `SELECT run_id,stage,ref,input_hash,state,epoch,attempts,batch FROM bs_items WHERE item_id=? AND checkout_id=?`, id, checkout).
		Scan(&it.RunID, &it.Stage, &it.Ref, &it.InputHash, &it.State, &it.Epoch, &it.Attempts, &it.Batch)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNoItem
	}
	return it, err
}

// Complete marks a leased item done. It runs fn in the same transaction, so the claims a
// submission produces and the item's new state commit or roll back together. An item that is
// not leased at exactly this epoch is refused with ErrStaleLease. Usage is added to the run.
// When the last sampled item of a sampling run completes, the run moves to awaiting-confirm.
func (r *Runs) Complete(ctx context.Context, itemID string, epoch int, u Usage, fn func(ctx context.Context, tx *sql.Tx, it Item) error) error {
	return r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		it, err := loadItem(ctx, tx, itemID, r.CheckoutID)
		if err != nil {
			return err
		}
		if it.State != "leased" || it.Epoch != epoch {
			return ErrStaleLease
		}
		if fn != nil {
			if err := fn(ctx, tx, it); err != nil {
				return err
			}
		}
		now := r.ts()
		if _, err := tx.ExecContext(ctx, `UPDATE bs_items SET state='done', done_at=? WHERE item_id=?`, now, itemID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bs_runs SET tokens_in=tokens_in+?, tokens_out=tokens_out+?, updated_at=? WHERE run_id=?`,
			u.InputTokens, u.OutputTokens, now, it.RunID); err != nil {
			return err
		}
		return settleSample(ctx, tx, it.RunID, now)
	})
}

// Fail returns a leased item to pending (or rejects it after MaxAttempts) when its submission
// was unusable as a whole.
func (r *Runs) Fail(ctx context.Context, itemID string, epoch int) error {
	return r.FailCounted(ctx, itemID, epoch, 0, 0)
}

// FailCounted is Fail that also adds the submission's accepted and rejected claim counts to the
// run in the same transaction, so a sample that settles on this failure freezes the final counts.
func (r *Runs) FailCounted(ctx context.Context, itemID string, epoch, accepted, rejected int) error {
	return r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		it, err := loadItem(ctx, tx, itemID, r.CheckoutID)
		if err != nil {
			return err
		}
		if it.State != "leased" || it.Epoch != epoch {
			return ErrStaleLease
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bs_runs SET accepted=accepted+?, rejected=rejected+? WHERE run_id=?`, accepted, rejected, it.RunID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE bs_items SET attempts=attempts+1, state=CASE WHEN attempts+1 >= ? THEN 'rejected' ELSE 'pending' END WHERE item_id=?`, MaxAttempts, itemID); err != nil {
			return err
		}
		return settleSample(ctx, tx, it.RunID, r.ts())
	})
}

// settleSample moves a sampling run to awaiting-confirm once no sampled extract item can still
// run (each is done or rejected), and freezes what the sample cost into sample_*: the full run
// goes on adding to the run's own counters, and the sample's figures must not move with them.
// It runs after every step that can close an item (Complete, Fail, lease expiry) and when the
// sample starts, so a sample whose last item is rejected, or one with no items at all, cannot
// strand the run in sampling.
func settleSample(ctx context.Context, tx *sql.Tx, runID, now string) error {
	var open int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bs_items WHERE run_id=? AND stage='extract' AND sampled=1 AND state IN ('pending','leased')`, runID).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE bs_runs SET state=?, sample_in=tokens_in, sample_out=tokens_out, sample_acc=accepted, sample_rej=rejected, updated_at=? WHERE run_id=? AND state=?`,
		StateAwaiting, now, runID, StateSampling)
	return err
}

// Status counts a run's items by stage and state and its sources by disposition.
func (r *Runs) Status(ctx context.Context, id string) (RunStatus, error) {
	st := RunStatus{Items: map[string]map[string]int{}, Dispositions: map[string]int{}, Claims: map[string]int{}}
	run, err := r.Get(ctx, id)
	if err != nil {
		return st, err
	}
	st.Run = run
	rows, err := r.DB.QueryContext(ctx, `SELECT stage,state,count(*) FROM bs_items WHERE run_id=? AND checkout_id=? GROUP BY stage,state`, id, r.CheckoutID)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var stage, state string
		var n int
		if err := rows.Scan(&stage, &state, &n); err != nil {
			rows.Close()
			return st, err
		}
		if st.Items[stage] == nil {
			st.Items[stage] = map[string]int{}
		}
		st.Items[stage][state] = n
	}
	rows.Close()
	rows, err = r.DB.QueryContext(ctx, `SELECT disposition,count(*) FROM bs_sources WHERE run_id=? AND checkout_id=? GROUP BY disposition`, id, r.CheckoutID)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			rows.Close()
			return st, err
		}
		st.Dispositions[d] = n
	}
	rows.Close()
	rows, err = r.DB.QueryContext(ctx, `SELECT status,count(*) FROM bs_claims WHERE run_id=? AND checkout_id=? GROUP BY status`, id, r.CheckoutID)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			rows.Close()
			return st, err
		}
		st.Claims[s] = n
	}
	rows.Close()
	err = r.DB.QueryRowContext(ctx, `SELECT count(*) FROM bs_components WHERE run_id=? AND checkout_id=?`, id, r.CheckoutID).Scan(&st.Components)
	return st, err
}

// Sources returns a run's source records in path order.
func (r *Runs) Sources(ctx context.Context, id string) ([]Source, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT source,path,oid,tier,hash,size,disposition,secret_spans,secret_lines,src_date FROM bs_sources
		WHERE run_id=? AND checkout_id=? ORDER BY source,path`, id, r.CheckoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var s Source
		var lines string
		if err := rows.Scan(&s.Name, &s.Path, &s.OID, &s.Tier, &s.Hash, &s.Size, &s.Disposition, &s.SecretSpans, &lines, &s.Date); err != nil {
			return nil, err
		}
		for _, l := range strings.Split(lines, ",") {
			if n, err := strconv.Atoi(l); err == nil {
				s.SecretLines = append(s.SecretLines, n)
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Components returns a run's components in rank order.
func (r *Runs) Components(ctx context.Context, id string) ([]Component, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT name,prefix,score,rank,inspected FROM bs_components WHERE run_id=? AND checkout_id=? ORDER BY rank`, id, r.CheckoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Component
	for rows.Next() {
		var c Component
		var insp int
		if err := rows.Scan(&c.Name, &c.Prefix, &c.Score, &c.Rank, &insp); err != nil {
			return nil, err
		}
		c.Inspected = insp == 1
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out, rows.Err()
}

// Item returns one item of this checkout.
func (r *Runs) Item(ctx context.Context, id string) (Item, error) {
	it := Item{ID: id}
	err := r.DB.QueryRowContext(ctx, `SELECT run_id,stage,ref,input_hash,state,epoch,attempts,batch FROM bs_items WHERE item_id=? AND checkout_id=?`, id, r.CheckoutID).
		Scan(&it.RunID, &it.Stage, &it.Ref, &it.InputHash, &it.State, &it.Epoch, &it.Attempts, &it.Batch)
	if errors.Is(err, sql.ErrNoRows) {
		return it, ErrNoItem
	}
	return it, err
}

// AddCounts adds to a run's accepted and rejected claim counts, for a submission that was
// requeued and so never reached Complete.
func (r *Runs) AddCounts(ctx context.Context, runID string, accepted, rejected int) error {
	return r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE bs_runs SET accepted=accepted+?, rejected=rejected+? WHERE run_id=? AND checkout_id=?`, accepted, rejected, runID, r.CheckoutID)
		return err
	})
}
