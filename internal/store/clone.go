package store

import (
	"context"
	"database/sql"
	"errors"
)

const cloneNonceKey = "clone-id:"

// CloneID returns the nonce recorded for a clone when it was registered. The same value must
// be found in the clone's git directory before its path is treated as that clone: a path
// alone is not an identity, because a deleted clone can be replaced by another repository.
func (s *Store) CloneID(ctx context.Context, commonDir string) (string, bool, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT v FROM meta WHERE k = ?`, cloneNonceKey+commonDir).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetCloneID records the nonce for a clone, replacing any earlier one.
func (s *Store) SetCloneID(ctx context.Context, commonDir, id string) error {
	return s.WithWriteLock(ctx, func(ctx context.Context) error {
		_, err := s.DB.ExecContext(ctx, `INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, cloneNonceKey+commonDir, id)
		return err
	})
}

// HasCloneRecords reports whether the store holds a trust decision or a registered worktree
// for commonDir.
func (s *Store) HasCloneRecords(ctx context.Context, commonDir string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM trust WHERE common_dir = ?) + (SELECT count(*) FROM checkouts WHERE common_dir = ?)`,
		commonDir, commonDir).Scan(&n)
	return n > 0, err
}

// perCheckoutTables are the tables keyed by a checkout id. job_ops hangs off jobs and is
// handled first.
var perCheckoutTables = []string{"turn_starts", "turns", "jobs", "pages", "page_notes", "claims", "fts"}

// RebindClone revokes everything the store holds for commonDir and binds it to newID, in one
// transaction: the trust decision, the registered worktrees and every row keyed by their
// checkout ids (turn_starts, turns, jobs, job_ops, pages, claims, fts). Revocation comes
// first and the new id second, and either both happen or neither, so no failure can leave the
// new id bound while the old trust survives. When a trust decision existed it is replaced by
// "undecided", never dropped to nothing, so the next init cannot mistake the path for a new
// clone it may trust on creation.
func (s *Store) RebindClone(ctx context.Context, commonDir, newID string) error {
	var old []string
	if err := s.WithWriteLock(ctx, func(ctx context.Context) (err error) {
		old, err = s.rebindTx(ctx, commonDir, newID)
		return err
	}); err != nil {
		return err
	}
	// The rows are gone and the notice will say the data was discarded, so the files must go
	// too. This is best effort and never undoes the revocation, and it is not database work,
	// so it runs after the write lock is released.
	s.purgeCheckoutFiles(old)
	return nil
}

// rebindTx is the transaction of RebindClone. It returns the checkout ids it discarded. The
// caller holds the database write lock.
func (s *Store) rebindTx(ctx context.Context, commonDir, newID string) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM checkouts WHERE common_dir = ?`, commonDir)
	if err != nil {
		return nil, err
	}
	var old []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		old = append(old, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range old {
		if _, err := tx.ExecContext(ctx, `DELETE FROM job_ops WHERE job_id IN (SELECT job_id FROM jobs WHERE checkout_id = ?)`, id); err != nil {
			return nil, err
		}
		for _, table := range perCheckoutTables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE checkout_id = ?`, id); err != nil {
				return nil, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM checkouts WHERE common_dir = ?`, commonDir); err != nil {
		return nil, err
	}
	var hadTrust int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM trust WHERE common_dir = ?`, commonDir).Scan(&hadTrust); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM trust WHERE common_dir = ?`, commonDir); err != nil {
		return nil, err
	}
	if hadTrust > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO trust(common_dir, decision, reason, at) VALUES(?, ?, ?, ?)`,
			commonDir, TrustUndecided, "clone could not be verified against the store's record", now()); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, cloneNonceKey+commonDir, newID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return old, nil
}
