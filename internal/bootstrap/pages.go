package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/TerrysPOV/Wikory/internal/publish"
	"github.com/TerrysPOV/Wikory/internal/redact"
)

// PlanOps decides what to publish. A new page is created only if it is still absent. A page
// whose content already equals the new content is left alone. A page that holds exactly what an
// earlier bootstrap wrote or was about to write (ours reports a recorded or pending hash for it,
// see Runs.IsOurs) is replaced against that exact content. Anything else is a page a person
// wrote or edited, and is reported as a conflict, never overwritten.
func PlanOps(pages []PageOut,
	current func(path string) (data []byte, exists bool, err error),
	ours func(path, hash string) bool) (ops []publish.Op, conflicts, unchanged []string, err error) {
	for _, pg := range pages {
		data, exists, err := current(pg.Path)
		if err != nil {
			return nil, nil, nil, err
		}
		if !exists {
			ops = append(ops, publish.Op{Target: pg.Path, OldHash: publish.Absent, Content: pg.Content})
			continue
		}
		cur := publish.HashContent(data)
		if cur == publish.HashContent(pg.Content) {
			unchanged = append(unchanged, pg.Path)
			continue
		}
		if ours(pg.Path, cur) {
			ops = append(ops, publish.Op{Target: pg.Path, OldHash: cur, Content: pg.Content})
			continue
		}
		conflicts = append(conflicts, pg.Path)
	}
	return ops, conflicts, unchanged, nil
}

// PageSecret names a rendered page that holds secret-shaped text and the kind of each span. It
// never holds the text.
type PageSecret struct {
	Path  string
	Kinds []string
}

// ScanPages scans every rendered page before anything is published. Claim text is checked when
// it is admitted, but a page is more than claims (component names, banners, listings), and this
// is the last place to stop a secret-shaped value from reaching the repository.
func ScanPages(pages []PageOut) []PageSecret {
	var out []PageSecret
	for _, pg := range pages {
		spans := redact.Scan(string(pg.Content))
		if len(spans) == 0 {
			continue
		}
		seen := map[string]bool{}
		var kinds []string
		for _, sp := range spans {
			if !seen[sp.Kind] {
				seen[sp.Kind] = true
				kinds = append(kinds, sp.Kind)
			}
		}
		sort.Strings(kinds)
		out = append(out, PageSecret{Path: pg.Path, Kinds: kinds})
	}
	return out
}

// PublishedHash is the hash of the content the last bootstrap wrote to path in this checkout
// and recorded as committed. A pending hash alone (see RecordPending) is not reported here.
func (r *Runs) PublishedHash(ctx context.Context, path string) (string, bool, error) {
	var h string
	err := r.DB.QueryRowContext(ctx, `SELECT hash FROM bs_pages WHERE checkout_id=? AND path=? AND hash != ''`, r.CheckoutID, path).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return h, err == nil, err
}

// IsOurs reports whether hash is what a bootstrap recorded for path in this checkout: the
// committed hash of its last publish, or the pending hash of a publish job that was about to run.
// A pending hash covers a finish killed between a job and the final record: the pages that job
// wrote are still recognised as Wikory's own, by this run or by any later one.
func (r *Runs) IsOurs(ctx context.Context, path, hash string) (bool, error) {
	if hash == "" {
		return false, nil
	}
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT count(*) FROM bs_pages WHERE checkout_id=? AND path=? AND (hash=? OR pending=?)`,
		r.CheckoutID, path, hash, hash).Scan(&n)
	return n > 0, err
}

// RecordPending stores, before a publish job begins, the hash each of its pages will have, in
// one transaction under the write lock. It never changes a committed hash: RecordPublished
// promotes a pending hash to committed once the run has written everything.
func (r *Runs) RecordPending(ctx context.Context, runID string, hashes map[string]string) error {
	return r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for p, h := range hashes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO bs_pages(checkout_id,path,hash,run_id,pending) VALUES(?,?,'',?,?)
				ON CONFLICT(checkout_id,path) DO UPDATE SET pending=excluded.pending`, r.CheckoutID, p, runID, h); err != nil {
				return err
			}
		}
		return nil
	})
}

// RecordPublished remembers the content hashes a publish wrote, so the next bootstrap can tell
// its own earlier output from a person's edit. Each becomes the committed hash and clears the
// path's pending hash.
func (r *Runs) RecordPublished(ctx context.Context, runID string, hashes map[string]string) error {
	return r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for p, h := range hashes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO bs_pages(checkout_id,path,hash,run_id,pending) VALUES(?,?,?,?,'')
				ON CONFLICT(checkout_id,path) DO UPDATE SET hash=excluded.hash, run_id=excluded.run_id, pending=''`, r.CheckoutID, p, h, runID); err != nil {
				return err
			}
		}
		return nil
	})
}
