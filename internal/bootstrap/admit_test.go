package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/validate"
)

type fakeEnv struct{ head map[string]string }

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
	o, ok := f.head[p]
	return o, ok, nil
}
func (fakeEnv) CommitExists(context.Context, string) (bool, error) { return true, nil }
func (fakeEnv) HeadCommit(context.Context) (string, error) {
	return "c0ffee0123456789c0ffee0123456789c0ffee01", nil
}
func (fakeEnv) ObjectFormat(context.Context) (string, error)                  { return "sha1", nil }
func (fakeEnv) ResolveObject(_ context.Context, id, _ string) (string, error) { return id, nil }

var oidA = strings.Repeat("a", 40)

func envWith(paths ...string) fakeEnv {
	e := fakeEnv{head: map[string]string{}}
	for _, p := range paths {
		e.head[p] = oidA
	}
	return e
}

func TestCapStatus(t *testing.T) {
	cases := []struct {
		s    Source
		want string
	}{
		{Source{Name: "repo", Path: "docs/design.md", Tier: TierAttributed}, "unverified"},
		{Source{Name: "repo", Path: "docs/decisions/0001.md", Tier: TierIntent}, "unverified"},
		{Source{Name: "repo", Path: ".claude/handoffs/PRIOR_SESSION_STATE.md", Tier: TierAttributed}, "historical"},
		{Source{Name: "nimbalyst", Path: "trackers/a.md", Tier: TierAttributed}, "historical"},
	}
	for _, c := range cases {
		if got := CapStatus(c.s); got != c.want {
			t.Errorf("CapStatus(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestAdmitMintsRendersAndParsesBack(t *testing.T) {
	ctx := context.Background()
	pc := ProposedClaim{Text: "Jobs are acknowledged only after the handler returns.", Kind: "current-impl"}
	a, err := Admit(ctx, envWith("docs/design.md"), pc, "unverified", []claim.Evidence{{Kind: claim.EvBlob, Path: "docs/design.md"}}, nil, TierAttributed, ids.NewULID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := claim.ParseComment(a.Meta)
	if err != nil {
		t.Fatalf("the rendered line must parse: %v\n%s", err, a.Meta)
	}
	if parsed.ID == "" || parsed.Status != "unverified" || parsed.Author != "agent" || parsed.TH != claim.TextHash(pc.Text) || parsed.Scope != "repo" {
		t.Fatalf("%+v", parsed)
	}
	if len(parsed.Evidence) != 1 || parsed.Evidence[0].OID == "" {
		t.Fatalf("committed blob evidence is resolved to a full id: %+v", parsed.Evidence)
	}
}

func TestAdmitNeverReturnsAVerifiedClaim(t *testing.T) {
	ctx := context.Background()
	pc := ProposedClaim{Text: "Jobs are acknowledged only after the handler returns.", Kind: "current-impl"}
	_, err := Admit(ctx, envWith("a.go"), pc, "verified", []claim.Evidence{{Kind: claim.EvBlob, Path: "a.go"}}, nil, TierImplementation, ids.NewULID)
	if err == nil {
		t.Fatal("extraction must refuse to produce a verified claim, even with committed code evidence")
	}
	if !IsRefusal(err) {
		t.Fatalf("a verified result is a verdict on the claim, not an infrastructure failure: %v", err)
	}
}

func TestIsRefusalSeparatesVerdictsFromInfrastructureFailures(t *testing.T) {
	for name, err := range map[string]error{
		"confinement": fmt.Errorf("%w: evidence %q", validate.ErrConfinement, "../x"),
		"ambiguous":   fmt.Errorf("%w: abc", validate.ErrAmbiguousID),
		"admit's own": fmt.Errorf("%w: it did not parse back", ErrRefused),
		"bare":        ErrRefused,
	} {
		if !IsRefusal(err) {
			t.Errorf("%s is a verdict on the claim and was not recognised as one", name)
		}
	}
	for name, err := range map[string]error{
		"git failure": errors.New("git: exit status 128"),
		"wrapped io":  fmt.Errorf("evidence a.go@abc: %w", io.ErrUnexpectedEOF),
		"context":     context.Canceled,
	} {
		if IsRefusal(err) {
			t.Errorf("%s is not a verdict on the claim but was counted as one", name)
		}
	}
}

func TestAdmitRefusesAnEscapingEvidencePath(t *testing.T) {
	pc := ProposedClaim{Text: "x is true.", Kind: "intent"}
	if _, err := Admit(context.Background(), envWith(), pc, "unverified", []claim.Evidence{{Kind: claim.EvBlob, Path: "../outside"}}, nil, TierAttributed, ids.NewULID); err == nil {
		t.Fatal("an evidence path that escapes the repository must be refused")
	}
}
