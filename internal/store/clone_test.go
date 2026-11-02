package store

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// trustedCheckout is a store with one registered checkout whose common directory is trusted.
func trustedCheckout(t *testing.T) (*Store, Identity) {
	t.Helper()
	ctx := context.Background()
	id := ident("wkp_P", "", "/c/.git", "/c")
	s := open(t, t.TempDir(), id, true)
	if _, err := s.Register(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTrust(ctx, id.CommonDir, TrustTrusted, "seeded"); err != nil {
		t.Fatal(err)
	}
	return s, id
}

// Revocation and binding are one transaction. When binding the new id fails, the old trust
// decision and registration must still be there: a half-done rebind must never be visible.
func TestRebindCloneIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s, id := trustedCheckout(t)
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
	s, id := trustedCheckout(t)
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

// Review L2: the notice tells the user the clone's private data was discarded, so the captured
// turn files and job directories of the old checkout ids must be gone, not just their rows.
func TestRebindCloneRemovesTheOldCheckoutsFilesOnDisk(t *testing.T) {
	ctx := context.Background()
	s, id := trustedCheckout(t)
	old := CheckoutID(id.CommonDir, id.Worktree, id.CloneID)
	other := "someone-elses-checkout"
	put := func(rel, data string) {
		t.Helper()
		if err := s.Root.MkdirAll(path.Dir(rel), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.Root.WriteFileAtomic(rel, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put("turns/"+old+"/t1/v0001.json", "{}")
	put("turns/"+old+"/t2/v0001.json", "{}")
	put("turns/"+other+"/t1/v0001.json", "{}")
	put("jobs/job-old/manifest.json", `{"checkout_id":"`+old+`"}`)
	put("jobs/job-old/staged/page.md", "x")
	put("jobs/job-other/manifest.json", `{"checkout_id":"`+other+`"}`)
	put("jobs/job-nomanifest/staged.md", "x")
	if err := s.RebindClone(ctx, id.CommonDir, "wkc_NEW"); err != nil {
		t.Fatal(err)
	}
	gone := func(rel string) bool {
		_, err := s.Root.ReadDir(rel)
		return err != nil
	}
	for _, rel := range []string{"turns/" + old, "jobs/job-old"} {
		if !gone(rel) {
			t.Errorf("%s survived a rebind that told the user the data was discarded", rel)
		}
	}
	for _, rel := range []string{"turns/" + other, "jobs/job-other", "jobs/job-nomanifest"} {
		if gone(rel) {
			t.Errorf("%s belongs to something else and was removed", rel)
		}
	}
	if len(s.Notices) != 0 {
		t.Errorf("a clean removal should add no notice: %v", s.Notices)
	}
}

// A file that cannot be removed is reported as a notice and never fails the revocation.
func TestRebindCloneReportsRemovalFailuresAsNoticesNotErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block removal the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	ctx := context.Background()
	s, id := trustedCheckout(t)
	old := CheckoutID(id.CommonDir, id.Worktree, id.CloneID)
	if err := s.Root.MkdirAll("turns/"+old+"/t1", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Root.WriteFileAtomic("turns/"+old+"/t1/v0001.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(s.Dir, "turns", old, "t1")
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	if err := s.RebindClone(ctx, id.CommonDir, "wkc_NEW"); err != nil {
		t.Fatalf("a removal failure failed the revocation: %v", err)
	}
	if d, _, _ := s.TrustDecision(ctx, id.CommonDir); d != TrustUndecided {
		t.Fatalf("trust = %q after a rebind with a stuck file", d)
	}
	found := false
	for _, n := range s.Notices {
		found = found || strings.Contains(n, "could not be removed")
	}
	if !found {
		t.Fatalf("no notice about the file that stayed: %v", s.Notices)
	}
}
