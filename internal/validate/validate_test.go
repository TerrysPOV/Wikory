package validate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/claim"
)

type fakeEnv struct {
	head    map[string]string // path -> blob at HEAD
	work    map[string]string // path -> current working blob ("" = absent)
	commits map[string]bool
	objects map[string][]string // short or full id -> matching full ids
}

func (f fakeEnv) HeadBlobs(_ context.Context, paths []string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range paths {
		if o, ok := f.head[p]; ok {
			out[p] = o
		}
	}
	return out, nil
}
func (f fakeEnv) WorkBlob(_ context.Context, p string) (string, bool, error) {
	o, ok := f.work[p]
	return o, ok && o != "", nil
}
func (f fakeEnv) CommitExists(_ context.Context, sha string) (bool, error) {
	return f.commits[sha], nil
}
func (f fakeEnv) HeadCommit(context.Context) (string, error) {
	return "c0ffee0123456789c0ffee0123456789c0ffee01", nil
}
func (f fakeEnv) ObjectFormat(context.Context) (string, error) { return "sha1", nil }
func (f fakeEnv) ResolveObject(_ context.Context, id, _ string) (string, error) {
	m := f.objects[id]
	switch len(m) {
	case 1:
		return m[0], nil
	case 0:
		return "", errors.New("not found")
	}
	return "", fmt.Errorf("%w: %s", ErrAmbiguousID, id)
}

const text = "Jobs are acknowledged only after the handler returns."

var oidA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
var oidB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func stamped(c claim.Claim) claim.Claim { c.TH = claim.TextHash(text); return c }

// blob is stamped (full sha1) evidence when oid is set, unstamped evidence when it is empty.
func blob(p, oid string) claim.Evidence {
	if oid == "" {
		return claim.Evidence{Kind: claim.EvBlob, Path: p}
	}
	return claim.Evidence{Kind: claim.EvBlob, Path: p, OID: oid, Algo: "sha1"}
}

func mint() string { return "01J8Q4ZK8N3X2T5V6W7Y8Z9A0B" }

func TestRule1AuthoringNoSelfEvidence(t *testing.T) {
	c := claim.Claim{Kind: "current-impl", Status: "verified", Evidence: []claim.Evidence{{Kind: claim.EvTurn, Ref: "t1"}}}
	out, f, err := Authoring(context.Background(), c, text, fakeEnv{}, mint)
	if err != nil || out.Kind != "hypothesis" || out.Status != "unverified" || !hasRule(f, 1) {
		t.Fatalf("out=%+v findings=%+v err=%v", out, f, err)
	}
}

func TestRule1DeriveSelfEvidenceIsHypothesis(t *testing.T) {
	c := stamped(claim.Claim{Kind: "current-impl", Status: "verified", Evidence: []claim.Evidence{{Kind: claim.EvTurn, Ref: "t1"}}})
	d, _ := Derive(context.Background(), c, text, fakeEnv{})
	if d.Status != "hypothesis" {
		t.Fatalf("%+v", d)
	}
}

func TestRule2UncommittedEvidenceStaysUnverified(t *testing.T) {
	// One committed and one uncommitted blob: resolvable evidence exists, so only the
	// uncommitted-evidence rule can keep this claim unverified.
	c := claim.Claim{Kind: "current-impl", Status: "verified", Evidence: []claim.Evidence{blob("src/q.go", ""), blob("src/new.go", "")}}
	out, f, err := Authoring(context.Background(), c, text, fakeEnv{head: map[string]string{"src/q.go": oidA}, work: map[string]string{"src/q.go": oidA, "src/new.go": oidB}}, mint)
	if err != nil || out.Status != "unverified" || out.Evidence[0].OID != oidA || out.Evidence[1].OID != "" || !hasRule(f, 2) {
		t.Fatalf("out=%+v f=%+v err=%v", out, f, err)
	}
}

func TestRule2And4AuthoringResolvesAndMints(t *testing.T) {
	c := claim.Claim{Kind: "current-impl", Status: "verified", Evidence: []claim.Evidence{blob("src/q.go", "")}, Deps: []string{"src/q.go#Ack"}}
	out, _, err := Authoring(context.Background(), c, text, fakeEnv{head: map[string]string{"src/q.go": oidA}, work: map[string]string{"src/q.go": oidA}}, mint)
	if err != nil || out.Status != "verified" || out.Evidence[0].OID != oidA || out.Evidence[0].Algo != "sha1" || out.ID != mint() ||
		out.TH != claim.TextHash(text) || out.Scope != "repo" || out.VRev != "c0ffee012345" || out.Author != "human" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestRule3StaleWhenDependencyChanged(t *testing.T) {
	c := stamped(claim.Claim{Kind: "current-impl", Status: "verified", VRev: "c0ffee0",
		Evidence: []claim.Evidence{blob("src/q.go", oidA)}, Deps: []string{"src/q.go#Ack"}})
	env := fakeEnv{head: map[string]string{"src/q.go": oidA}, work: map[string]string{"src/q.go": oidB}}
	d, _ := Derive(context.Background(), c, text, env)
	if !d.Stale || !strings.HasPrefix(d.Freshness, "STALE: src/q.go") {
		t.Fatalf("%+v", d)
	}
	env.work["src/q.go"] = oidA
	d, _ = Derive(context.Background(), c, text, env)
	if d.Stale || d.Status != "verified" || d.Freshness != "verified @c0ffee0, sources unchanged" {
		t.Fatalf("unchanged: %+v", d)
	}
	delete(env.work, "src/q.go")
	d, _ = Derive(context.Background(), c, text, env)
	if d.Freshness != "source removed: src/q.go" {
		t.Fatalf("removed: %+v", d)
	}
}

func TestRule5HumanEditDropsToUnverified(t *testing.T) {
	c := stamped(claim.Claim{Kind: "current-impl", Status: "verified", Evidence: []claim.Evidence{blob("src/q.go", oidA)}})
	env := fakeEnv{head: map[string]string{"src/q.go": oidA}, work: map[string]string{"src/q.go": oidA}}
	d, _ := Derive(context.Background(), c, text+" Edited by hand.", env)
	if !d.Edited || d.Status != "unverified" {
		t.Fatalf("%+v", d)
	}
}

func TestRule6CommittedVerifiedIsReDerived(t *testing.T) {
	c := stamped(claim.Claim{Kind: "current-impl", Status: "verified", Evidence: []claim.Evidence{blob("src/q.go", oidA)}})
	env := fakeEnv{head: map[string]string{"src/q.go": oidB}, work: map[string]string{"src/q.go": oidB}}
	d, _ := Derive(context.Background(), c, text, env)
	if d.Status != "asserted verified, not locally confirmed" {
		t.Fatalf("%+v", d)
	}
}

func TestRule7AuthoringRefusesEscapingPaths(t *testing.T) {
	for _, p := range []string{"../etc/passwd", "/etc/passwd", `src\q.go`} {
		c := claim.Claim{Kind: "intent", Status: "unverified", Evidence: []claim.Evidence{blob(p, "")}}
		if _, _, err := Authoring(context.Background(), c, text, fakeEnv{}, mint); !errors.Is(err, ErrConfinement) {
			t.Errorf("%q: want ErrConfinement, got %v", p, err)
		}
		c = claim.Claim{Kind: "intent", Status: "unverified", Deps: []string{p}}
		if _, _, err := Authoring(context.Background(), c, text, fakeEnv{}, mint); !errors.Is(err, ErrConfinement) {
			t.Errorf("dep %q: want ErrConfinement, got %v", p, err)
		}
	}
}

func TestExternalOnlyCannotVerify(t *testing.T) {
	c := claim.Claim{Kind: "intent", Status: "verified", Evidence: []claim.Evidence{{Kind: claim.EvExternal, Ref: "https://example.invalid"}}}
	out, _, _ := Authoring(context.Background(), c, text, fakeEnv{}, mint)
	if out.Status != "unverified" {
		t.Fatalf("%+v", out)
	}
	d, _ := Derive(context.Background(), stamped(c), text, fakeEnv{})
	if d.Status == "verified" || !strings.Contains(strings.Join(d.Notes, ","), "external") {
		t.Fatalf("%+v", d)
	}
}

func TestUnstampedIsUnverified(t *testing.T) {
	c := claim.Claim{Kind: "intent", Status: "verified", Evidence: []claim.Evidence{blob("src/q.go", oidA)}}
	d, _ := Derive(context.Background(), c, text, fakeEnv{head: map[string]string{"src/q.go": oidA}})
	if d.Status != "unverified" {
		t.Fatalf("%+v", d)
	}
}

func hasRule(f []Finding, rule int) bool {
	for _, x := range f {
		if x.Rule == rule {
			return true
		}
	}
	return false
}

// Rule 2 for a tracked file: committed, but the working copy differs from HEAD. A teammate's
// clone would see the committed blob, not what was in front of the author, so it cannot verify.
func TestRule2DirtyTrackedEvidenceStaysUnverified(t *testing.T) {
	c := claim.Claim{Kind: "current-impl", Status: "verified", Evidence: []claim.Evidence{blob("src/q.go", "")}, Deps: []string{"src/q.go"}}
	env := fakeEnv{head: map[string]string{"src/q.go": oidA}, work: map[string]string{"src/q.go": oidB}}
	out, f, err := Authoring(context.Background(), c, text, env, mint)
	if err != nil || out.Status != "unverified" || out.Evidence[0].OID != "" || !hasCode(f, "dirty-evidence") {
		t.Fatalf("out=%+v findings=%+v err=%v", out, f, err)
	}
}

func TestShortEvidenceIsResolvedAndAmbiguityIsAnError(t *testing.T) {
	env := fakeEnv{head: map[string]string{"src/q.go": oidA}, work: map[string]string{"src/q.go": oidA},
		objects: map[string][]string{"aaaaaaa": {oidA}, "bbbbbbb": {oidB, "bbbbbbbcccccccccccccccccccccccccccccccc"}}}
	c := claim.Claim{Kind: "current-impl", Status: "verified", Evidence: []claim.Evidence{{Kind: claim.EvBlob, Path: "src/q.go", OID: "aaaaaaa"}}}
	out, _, err := Authoring(context.Background(), c, text, env, mint)
	if err != nil || out.Evidence[0].OID != oidA || out.Evidence[0].Algo != "sha1" || out.Status != "verified" {
		t.Fatalf("short id must resolve to the full committed id: %+v %v", out, err)
	}
	c.Evidence[0].OID = "bbbbbbb"
	if _, _, err := Authoring(context.Background(), c, text, env, mint); !errors.Is(err, ErrAmbiguousID) {
		t.Fatalf("an ambiguous short id must be an error, got %v", err)
	}
}

func TestDeriveNeverConfirmsAShortID(t *testing.T) {
	c := stamped(claim.Claim{Kind: "current-impl", Status: "verified", Evidence: []claim.Evidence{{Kind: claim.EvBlob, Path: "src/q.go", OID: oidA[:12]}}})
	env := fakeEnv{head: map[string]string{"src/q.go": oidA}, work: map[string]string{"src/q.go": oidA}}
	if d, _ := Derive(context.Background(), c, text, env); d.Status != "asserted verified, not locally confirmed" {
		t.Fatalf("a prefix must never confirm evidence: %+v", d)
	}
}

func hasCode(f []Finding, code string) bool {
	for _, x := range f {
		if x.Code == code {
			return true
		}
	}
	return false
}
