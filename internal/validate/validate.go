// Package validate enforces the claim rules of spec 5.5 that increment 1a needs.
package validate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/confine"
)

// ErrConfinement: a claim names a path outside the repository root (rule 7).
var ErrConfinement = errors.New("validate: claim path is not a clean repository-relative path")

// ErrAmbiguousID is what an Env's ResolveObject wraps when a short id matches more than one
// object. Authoring refuses such evidence; it never guesses.
var ErrAmbiguousID = errors.New("validate: evidence id is ambiguous")

// Env is what the rules need to know about the checkout. Object ids are always full.
type Env interface {
	HeadBlobs(ctx context.Context, paths []string) (map[string]string, error)
	WorkBlob(ctx context.Context, path string) (oid string, present bool, err error)
	CommitExists(ctx context.Context, sha string) (bool, error)
	HeadCommit(ctx context.Context) (string, error)
	ObjectFormat(ctx context.Context) (string, error)
	// ResolveObject returns the unique full id for id, or an error wrapping ErrAmbiguousID.
	ResolveObject(ctx context.Context, id, kind string) (string, error)
}

// Finding records why a rule changed or refused a claim.
type Finding struct {
	Rule int
	Code string
	Msg  string
}

// Display is how retrieval shows a claim in this checkout.
type Display struct {
	Status    string
	Freshness string
	Stale     bool
	Edited    bool
	Notes     []string
}

const asserted = "asserted verified, not locally confirmed"

func thMatches(text, th string) bool {
	h := claim.TextHash(text)
	n := min(len(h), len(th))
	return n >= 6 && h[:n] == th[:n]
}

func evidenceShape(c claim.Claim) (blobs []string, onlySelf, onlyExternal bool) {
	self, ext := 0, 0
	for _, e := range c.Evidence {
		switch e.Kind {
		case claim.EvBlob:
			blobs = append(blobs, e.Path)
		case claim.EvTurn:
			self++
		case claim.EvExternal:
			ext++
		}
	}
	n := len(c.Evidence)
	return blobs, n > 0 && self == n, n > 0 && ext == n
}

// Authoring applies rules 1, 2, 4 and 7 when the binary renders a claim's metadata. Every
// object id it writes is the full id with its algorithm; a short id is resolved first and
// an ambiguous one is an error.
func Authoring(ctx context.Context, c claim.Claim, text string, env Env, mint func() string) (claim.Claim, []Finding, error) {
	var findings []Finding
	note := func(rule int, code, format string, a ...any) {
		findings = append(findings, Finding{Rule: rule, Code: code, Msg: fmt.Sprintf(format, a...)})
	}
	out := c
	out.Evidence = append([]claim.Evidence(nil), c.Evidence...)
	// Rule 7: every named path must be confinable before anything is resolved.
	for _, e := range out.Evidence {
		if e.Kind == claim.EvBlob {
			if _, err := confine.Split(e.Path); err != nil {
				return claim.Claim{}, nil, fmt.Errorf("%w: evidence %q", ErrConfinement, e.Path)
			}
		}
	}
	for _, p := range c.DepPaths() {
		if _, err := confine.Split(p); err != nil {
			return claim.Claim{}, nil, fmt.Errorf("%w: dependency %q", ErrConfinement, p)
		}
	}
	algo, err := env.ObjectFormat(ctx)
	if err != nil {
		return claim.Claim{}, nil, err
	}
	blobs, onlySelf, onlyExternal := evidenceShape(out)
	head, err := env.HeadBlobs(ctx, blobs)
	if err != nil {
		return claim.Claim{}, nil, err
	}
	unresolved, resolvable := 0, 0
	// unresolve drops the object id of blob evidence that cannot be confirmed, so none is written.
	unresolve := func(i int) {
		out.Evidence[i].OID, out.Evidence[i].Algo = "", ""
		unresolved++
	}
	for i, e := range out.Evidence {
		switch e.Kind {
		case claim.EvBlob:
			headOID, committed := head[e.Path]
			if !committed {
				unresolve(i)
				note(2, "uncommitted-evidence", "evidence %s is not committed at HEAD; the claim stays unverified", e.Path)
				continue
			}
			if work, present, err := env.WorkBlob(ctx, e.Path); err != nil {
				return claim.Claim{}, nil, err
			} else if !present || work != headOID {
				unresolve(i)
				note(2, "dirty-evidence", "evidence %s has uncommitted changes; the claim stays unverified until they are committed", e.Path)
				continue
			}
			if e.OID != "" {
				full, err := env.ResolveObject(ctx, e.OID, "blob")
				if errors.Is(err, ErrAmbiguousID) {
					return claim.Claim{}, nil, fmt.Errorf("evidence %s@%s: %w", e.Path, e.OID, err)
				}
				if err != nil || full != headOID {
					unresolve(i)
					note(2, "stale-evidence", "evidence %s names blob %s, which is not the committed version", e.Path, e.OID)
					continue
				}
			}
			out.Evidence[i].OID, out.Evidence[i].Algo = headOID, algo
			resolvable++
		case claim.EvCommit:
			full, err := env.ResolveObject(ctx, e.Ref, "commit")
			if errors.Is(err, ErrAmbiguousID) {
				return claim.Claim{}, nil, fmt.Errorf("evidence commit %s: %w", e.Ref, err)
			}
			if err != nil {
				unresolved++
				note(2, "unknown-commit", "commit %s does not resolve", e.Ref)
				continue
			}
			out.Evidence[i].Ref, out.Evidence[i].Algo = full, algo
			resolvable++
		}
	}
	// Rule 1: an agent summary alone is never verified and never current-impl.
	if onlySelf && out.Kind == "current-impl" {
		out.Kind = "hypothesis"
		note(1, "self-evidence-kind", "only agent-summary evidence: current-impl becomes hypothesis")
	}
	if out.Status == "verified" {
		switch {
		case onlySelf:
			out.Status = "unverified"
			note(1, "self-evidence-status", "only agent-summary evidence cannot verify")
		case onlyExternal:
			out.Status = "unverified"
			note(2, "external-only", "external evidence alone cannot verify")
		case unresolved > 0:
			out.Status = "unverified"
		case resolvable == 0:
			out.Status = "unverified"
			note(2, "no-evidence", "no resolvable evidence")
		}
	}
	// Rule 4: the binary mints the id, hash, scope and revision.
	if out.ID == "" {
		out.ID = mint()
	}
	out.Scope = "repo"
	if out.Author == "" {
		out.Author = "human"
	}
	out.TH = claim.TextHash(text)
	out.VRev = ""
	if out.Status == "verified" {
		headCommit, err := env.HeadCommit(ctx)
		if err != nil {
			return claim.Claim{}, nil, err
		}
		if len(headCommit) >= 12 {
			out.VRev = headCommit[:12] // a hint for people, never identity (spec 5.3)
		}
	}
	return out, findings, nil
}

// Derive applies rules 1, 2, 3, 5 and 6 at read time. Only full ids of this repository's
// algorithm can confirm anything; a short or foreign id is never compared by prefix.
func Derive(ctx context.Context, c claim.Claim, text string, env Env) (Display, error) {
	var d Display
	algo, err := env.ObjectFormat(ctx)
	if err != nil {
		return Display{}, err
	}
	blobs, onlySelf, onlyExternal := evidenceShape(c)
	for _, e := range c.Evidence {
		if e.Kind == claim.EvExternal {
			d.Notes = append(d.Notes, "external")
			break
		}
	}
	switch {
	case c.TH == "":
		d.Status = "unverified"
		d.Notes = append(d.Notes, "unstamped: run wikory page stamp")
	case !thMatches(text, c.TH):
		d.Status, d.Edited = "unverified", true
		d.Notes = append(d.Notes, "edited by a human since verification")
	case c.Status == "verified" && onlySelf:
		d.Status = "unverified"
		d.Notes = append(d.Notes, "agent summary is its only evidence")
	case c.Status == "verified" && onlyExternal:
		d.Status = "unverified"
	case c.Status == "verified":
		head, err := env.HeadBlobs(ctx, blobs)
		if err != nil {
			return Display{}, err
		}
		confirmed, resolvable := true, 0
		for _, e := range c.Evidence {
			switch e.Kind {
			case claim.EvBlob:
				if e.Algo == algo && e.OID != "" && head[e.Path] == e.OID {
					resolvable++
				} else {
					confirmed = false
				}
			case claim.EvCommit:
				if ok, _ := env.CommitExists(ctx, e.Ref); ok && e.Algo == algo {
					resolvable++
				} else {
					confirmed = false
				}
			}
		}
		if confirmed && resolvable > 0 {
			d.Status = "verified"
		} else {
			d.Status = asserted
		}
	default:
		d.Status = c.Status
	}
	if err := freshness(ctx, c, algo, env, &d); err != nil {
		return Display{}, err
	}
	return d, nil
}

// freshness computes staleness from current hashes (rule 3); nothing is stored.
func freshness(ctx context.Context, c claim.Claim, algo string, env Env, d *Display) error {
	deps := c.DepPaths()
	if len(deps) == 0 {
		d.Freshness = "no dependencies recorded"
		return nil
	}
	evOID := map[string]string{}
	for _, e := range c.Evidence {
		if e.Kind == claim.EvBlob && e.OID != "" && e.Algo == algo {
			evOID[e.Path] = e.OID
		}
	}
	var stale, removed, unchecked []string
	for _, p := range deps {
		want, ok := evOID[p]
		if !ok {
			unchecked = append(unchecked, p)
			continue
		}
		cur, present, err := env.WorkBlob(ctx, p)
		if err != nil {
			return err
		}
		switch {
		case !present:
			removed = append(removed, p)
		case cur != want:
			stale = append(stale, p)
		}
	}
	switch {
	case len(stale) > 0:
		d.Stale = true
		d.Freshness = "STALE: " + strings.Join(stale, ", ") + " changed since verification"
	case len(removed) > 0:
		d.Stale = true
		d.Freshness = "source removed: " + strings.Join(removed, ", ")
	case len(unchecked) == len(deps):
		d.Freshness = "freshness unknown"
	default:
		d.Freshness = "sources unchanged"
		if len(unchecked) > 0 { // some dependencies have no recorded baseline, so they were not compared
			d.Freshness = "unchanged where checked; no baseline for: " + strings.Join(unchecked, ", ")
		}
		if d.Status == "verified" && c.VRev != "" {
			d.Freshness = "verified @" + c.VRev + ", " + d.Freshness
		}
	}
	return nil
}
