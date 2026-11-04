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
// whose content already equals the new content is left alone. A page that holds exactly what
// the previous bootstrap wrote (its recorded hash) is replaced against that exact content.
// Anything else is a page a person wrote or edited, and is reported as a conflict, never
// overwritten.
func PlanOps(pages []PageOut,
	current func(path string) (data []byte, exists bool, err error),
	recorded func(path string) (hash string, ok bool)) (ops []publish.Op, conflicts, unchanged []string, err error) {
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
		if rec, ok := recorded(pg.Path); ok && rec == cur {
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

// PublishedHash is the hash of the content the last bootstrap wrote to path in this checkout.
func (r *Runs) PublishedHash(ctx context.Context, path string) (string, bool, error) {
	var h string
	err := r.DB.QueryRowContext(ctx, `SELECT hash FROM bs_pages WHERE checkout_id=? AND path=?`, r.CheckoutID, path).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return h, err == nil, err
}

// RecordPublished remembers the content hashes a publish wrote, so the next bootstrap can tell
// its own earlier output from a person's edit.
func (r *Runs) RecordPublished(ctx context.Context, runID string, hashes map[string]string) error {
	return r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for p, h := range hashes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO bs_pages(checkout_id,path,hash,run_id) VALUES(?,?,?,?)
				ON CONFLICT(checkout_id,path) DO UPDATE SET hash=excluded.hash, run_id=excluded.run_id`, r.CheckoutID, p, h, runID); err != nil {
				return err
			}
		}
		return nil
	})
}
