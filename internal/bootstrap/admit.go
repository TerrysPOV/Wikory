package bootstrap

import (
	"context"
	"errors"
	"fmt"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/validate"
)

// CapStatus is the highest status a claim extracted from s may carry. Handoffs, memory notes
// and anything read from outside the repository describe the past: historical. Everything else
// is unverified until code evidence says otherwise. Nothing extracted is ever verified.
func CapStatus(s Source) string {
	if s.Name != "repo" || IsHandoff(s.Path) {
		return "historical"
	}
	return "unverified"
}

// EvidenceFor is the evidence a claim extracted from a whole source carries: the committed
// blob for a repository file, an external reference for anything else.
func EvidenceFor(s Source) []claim.Evidence {
	if s.Name == "repo" {
		return []claim.Evidence{{Kind: claim.EvBlob, Path: s.Path}}
	}
	return []claim.Evidence{{Kind: claim.EvExternal, Ref: s.Name + ":" + s.Path}}
}

// Admitted is a claim ready to store: its rendered metadata line and its paragraph.
type Admitted struct {
	Claim    claim.Claim
	Text     string
	Meta     string
	Tier     string
	Findings []validate.Finding
}

// Admit runs a proposed claim through validate.Authoring (author is "agent": the claim grammar allows only agent or human), which mints the id, text hash and
// scope and resolves evidence to full object ids. It refuses a result that is verified and one
// whose rendering the parser would not read back.
func Admit(ctx context.Context, env validate.Env, pc ProposedClaim, status string, ev []claim.Evidence, deps []string, tier string, mint func() string) (Admitted, error) {
	c := claim.Claim{Kind: pc.Kind, Status: status, Evidence: ev, Deps: deps, Author: "agent"}
	out, findings, err := validate.Authoring(ctx, c, pc.Text, env, mint)
	if err != nil {
		return Admitted{}, err
	}
	if out.Status == "verified" {
		return Admitted{}, errors.New("bootstrap: an extracted claim came out verified; only a code-evidenced verdict may do that")
	}
	meta := out.Render()
	if _, err := claim.ParseComment(meta); err != nil {
		return Admitted{}, fmt.Errorf("bootstrap: the rendered claim does not parse back: %w", err)
	}
	return Admitted{Claim: out, Text: pc.Text, Meta: meta, Tier: tier, Findings: findings}, nil
}
