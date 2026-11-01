package store

import (
	"context"
	"database/sql"
	"errors"
)

// Trust decisions recorded in 1a. The full ledger and transition table are 1d (spec 10.4).
const (
	TrustTrusted   = "trusted"
	TrustUndecided = "undecided"
)

// RecordTrust records the decision for a clone (its git common directory).
func (s *Store) RecordTrust(ctx context.Context, commonDir, decision, reason string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO trust(common_dir, decision, reason, at) VALUES(?, ?, ?, ?)
		ON CONFLICT(common_dir) DO UPDATE SET decision = excluded.decision, reason = excluded.reason, at = excluded.at`,
		commonDir, decision, reason, now())
	return err
}

// TrustDecision returns the recorded decision, if any.
func (s *Store) TrustDecision(ctx context.Context, commonDir string) (string, bool, error) {
	var d string
	err := s.DB.QueryRowContext(ctx, `SELECT decision FROM trust WHERE common_dir = ?`, commonDir).Scan(&d)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return d, err == nil, err
}
