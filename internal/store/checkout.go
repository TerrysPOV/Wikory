package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"

	"github.com/TerrysPOV/Wikory/internal/ids"
)

// Checkout is one registered worktree of one clone.
type Checkout struct {
	ID        string
	CommonDir string
	Worktree  string
}

// CheckoutID identifies a worktree within a clone.
func CheckoutID(commonDir, worktree string) string {
	return ids.Digest("checkout", commonDir, worktree)
}

// Register records the checkout and its common directory. Callers decide trust first.
func (s *Store) Register(ctx context.Context, id Identity) (Checkout, error) {
	c := Checkout{ID: CheckoutID(id.CommonDir, id.Worktree), CommonDir: id.CommonDir, Worktree: id.Worktree}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO checkouts(id, common_dir, worktree, registered_at) VALUES(?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`, c.ID, c.CommonDir, c.Worktree, now()); err != nil {
		return Checkout{}, err
	}
	var ownErr error
	err := s.updateRegistry(func(r *registry) bool {
		addedDir := r.addCommonDir(id.CommonDir)
		addedOrigin := false
		if id.Origin != "" && !slices.Contains(r.Origins, id.Origin) {
			// Never alias an origin another store owns (see Resolve).
			var owned bool
			if owned, ownErr = originOwnedElsewhere(filepath.Join(s.DataDir, "stores"), r.ProjectID, s.Key, id.Origin); owned {
				s.Notices = append(s.Notices, aliasNotice(id.Origin))
			} else if ownErr == nil {
				addedOrigin = r.addOrigin(id.Origin)
			}
		}
		return addedDir || addedOrigin
	})
	if err == nil {
		err = ownErr
	}
	return c, err
}

// LookupCheckout reports whether this exact worktree is registered.
func (s *Store) LookupCheckout(ctx context.Context, id Identity) (Checkout, bool, error) {
	c := Checkout{ID: CheckoutID(id.CommonDir, id.Worktree)}
	err := s.DB.QueryRowContext(ctx, `SELECT common_dir, worktree FROM checkouts WHERE id = ?`, c.ID).Scan(&c.CommonDir, &c.Worktree)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkout{}, false, nil
	}
	if err != nil {
		return Checkout{}, false, err
	}
	return c, true, nil
}

// CommonDirRegistered reports whether any worktree of this clone is registered.
func (s *Store) CommonDirRegistered(ctx context.Context, commonDir string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM checkouts WHERE common_dir = ?`, commonDir).Scan(&n)
	return n > 0, err
}
