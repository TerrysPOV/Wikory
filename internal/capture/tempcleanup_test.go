package capture

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/decode"
)

// A hook killed between CreateExclusive's link and its removal of the temporary name leaves the
// version with two links, which confinement refuses to read. The next read removes the
// leftover temporary name and succeeds; any other extra link stays refused.
func TestReadVersionReclaimsALeftoverTempLink(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	_ = r.rec.TurnStart(ctx, r.ev(decode.UserPrompt, "hello", "p1", ""))
	id, _, _ := r.rec.Stop(ctx, r.ev(decode.Stop, "", "p1", "never written"))
	dir := filepath.Join(r.rec.Store.Dir, "turns", r.rec.CheckoutID, id)
	version := filepath.Join(dir, "v0001.json")

	temp := filepath.Join(dir, confine.TempPrefix+"v0001.json-0123456789abcdef")
	if err := os.Link(version, temp); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if _, err := r.rec.readVersion(id, 1); err != nil {
		t.Fatalf("a leftover temporary link blocked the read: %v", err)
	}
	if _, err := os.Lstat(temp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the leftover temporary link was not removed: %v", err)
	}

	other := filepath.Join(dir, "elsewhere.json")
	if err := os.Link(version, other); err != nil {
		t.Fatal(err)
	}
	if _, err := r.rec.readVersion(id, 1); !errors.Is(err, confine.ErrHardlink) {
		t.Fatalf("a hard link that is not a temporary name must stay refused, got %v", err)
	}
}
