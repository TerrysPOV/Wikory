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
	_, err := s.DB.ExecContext(ctx, `INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, cloneNonceKey+commonDir, id)
	return err
}

// ForgetClone drops what the store holds about a common directory whose repository has been
// replaced: its trust decision and its registered worktrees.
func (s *Store) ForgetClone(ctx context.Context, commonDir string) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM trust WHERE common_dir = ?`, commonDir); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM checkouts WHERE common_dir = ?`, commonDir)
	return err
}
