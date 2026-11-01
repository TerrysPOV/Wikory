package ids

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestULIDVectors(t *testing.T) {
	zero, err := ULID(time.UnixMilli(0), bytes.NewReader(make([]byte, 10)))
	if err != nil || zero != "00000000000000000000000000" {
		t.Fatalf("zero vector: %q %v", zero, err)
	}
	max, err := ULID(time.UnixMilli(1<<48-1), bytes.NewReader(bytes.Repeat([]byte{0xFF}, 10)))
	if err != nil || max != "7ZZZZZZZZZZZZZZZZZZZZZZZZZ" {
		t.Fatalf("max vector: %q %v", max, err)
	}
	if _, err := ULID(time.UnixMilli(1<<48), bytes.NewReader(make([]byte, 10))); err == nil {
		t.Fatal("a timestamp beyond 48 bits must be refused")
	}
}

func TestULIDSortsByTime(t *testing.T) {
	a, _ := ULID(time.UnixMilli(1_700_000_000_000), bytes.NewReader(bytes.Repeat([]byte{0xFF}, 10)))
	b, _ := ULID(time.UnixMilli(1_700_000_000_001), bytes.NewReader(make([]byte, 10)))
	if !(a < b) {
		t.Fatalf("later timestamp must sort later: %s !< %s", a, b)
	}
}

func TestPrefixedIDs(t *testing.T) {
	p, j := ProjectID(), JobID()
	if !strings.HasPrefix(p, "wkp_") || !IsULID(p[4:]) || !strings.HasPrefix(j, "job_") || !IsULID(j[4:]) {
		t.Fatalf("p=%q j=%q", p, j)
	}
	if IsULID("0000000000000000000000000I") || IsULID("short") {
		t.Fatal("IsULID accepted an invalid value")
	}
}

func TestDigestBoundaries(t *testing.T) {
	if Digest("ab", "c") == Digest("a", "bc") {
		t.Fatal("part boundaries must change the digest")
	}
	if len(Digest("x")) != 32 || len(SHA256Hex([]byte("x"))) != 64 {
		t.Fatal("unexpected digest length")
	}
}
