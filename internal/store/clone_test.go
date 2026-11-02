package store

import (
	"context"
	"testing"
)

// Revocation and binding are one transaction. When binding the new id fails, the old trust
// decision and registration must still be there: a half-done rebind must never be visible.
func TestRebindCloneIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	id := ident("wkp_P", "", "/c/.git", "/c")
	s := open(t, t.TempDir(), id, true)
	if _, err := s.Register(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTrust(ctx, id.CommonDir, TrustTrusted, "seeded"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER refuse_clone_id BEFORE INSERT ON meta WHEN NEW.k LIKE 'clone-id:%'
		BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RebindClone(ctx, id.CommonDir, "wkc_NEW"); err == nil {
		t.Fatal("the rebind should have failed")
	}
	if d, ok, _ := s.TrustDecision(ctx, id.CommonDir); !ok || d != TrustTrusted {
		t.Fatalf("a failed rebind changed the trust decision: %q %v", d, ok)
	}
	if known, _ := s.CommonDirRegistered(ctx, id.CommonDir); !known {
		t.Fatal("a failed rebind dropped the registration")
	}
}

func TestRebindCloneRevokesTrustToUndecidedAndBinds(t *testing.T) {
	ctx := context.Background()
	id := ident("wkp_P", "", "/c/.git", "/c")
	s := open(t, t.TempDir(), id, true)
	if _, err := s.Register(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTrust(ctx, id.CommonDir, TrustTrusted, "seeded"); err != nil {
		t.Fatal(err)
	}
	if err := s.RebindClone(ctx, id.CommonDir, "wkc_NEW"); err != nil {
		t.Fatal(err)
	}
	if d, ok, _ := s.TrustDecision(ctx, id.CommonDir); !ok || d != TrustUndecided {
		t.Fatalf("trust after a rebind = %q (recorded %v), want undecided", d, ok)
	}
	if known, _ := s.CommonDirRegistered(ctx, id.CommonDir); known {
		t.Fatal("the old registration survived a rebind")
	}
	if got, ok, _ := s.CloneID(ctx, id.CommonDir); !ok || got != "wkc_NEW" {
		t.Fatalf("clone id = %q %v", got, ok)
	}
}
