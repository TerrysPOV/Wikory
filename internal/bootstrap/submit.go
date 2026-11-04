package bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/validate"
)

// SubmitInput is one extract submission.
type SubmitInput struct {
	Runs  *Runs
	Env   validate.Env
	Work  WorkFiles
	Mint  func() string
	Batch string
	P     Proposal
}

// SubmitReport says what became of one submission.
type SubmitReport struct {
	Item     string   `json:"item"`
	Accepted int      `json:"accepted"`
	Rejected []Reject `json:"rejected"`
	Findings []string `json:"findings"`
	Requeued bool     `json:"requeued"`
}

// StoredClaim is a claim row of a run.
type StoredClaim struct {
	Item, ClaimID, Text, Kind, Status, Meta, Tier, Ref, Loc string
	Ordinal                                                 int
}

// Claims returns every claim of a run with the ref of the item that produced it.
func (r *Runs) Claims(ctx context.Context, runID string) ([]StoredClaim, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT c.item_id,c.ordinal,c.claim_id,c.text,c.kind,c.status,c.meta,c.tier,c.loc,i.ref
		FROM bs_claims c JOIN bs_items i ON i.item_id=c.item_id WHERE c.run_id=? AND c.checkout_id=? ORDER BY c.claim_id`, runID, r.CheckoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredClaim
	for rows.Next() {
		var s StoredClaim
		if err := rows.Scan(&s.Item, &s.Ordinal, &s.ClaimID, &s.Text, &s.Kind, &s.Status, &s.Meta, &s.Tier, &s.Loc, &s.Ref); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// claimBasis is what an item's claims are grounded in.
type claimBasis struct {
	src   Source            // for src: items
	comp  bool              // for comp: items
	files map[string]Source // component files by path
	secs  []Section
	srcs  []Source
}

func basisFor(ctx context.Context, runs *Runs, it Item, work []byte) (claimBasis, error) {
	srcs, err := runs.Sources(ctx, it.RunID)
	if err != nil {
		return claimBasis{}, err
	}
	b := claimBasis{srcs: srcs}
	if name, p, ok := splitSrcRef(it.Ref); ok {
		for _, s := range srcs {
			if s.Name == name && s.Path == p {
				b.src = s
				return b, nil
			}
		}
		return b, fmt.Errorf("bootstrap: item %s names a source that is not in the run", it.Ref)
	}
	comps, err := runs.Components(ctx, it.RunID)
	if err != nil {
		return b, err
	}
	prefix := strings.TrimPrefix(it.Ref, "comp:")
	b.comp, b.files = true, map[string]Source{}
	valid := map[string]string{}
	for _, s := range ComponentFiles(Result{Sources: srcs, Components: comps})[prefix] {
		b.files[s.Path] = s
		valid[s.Path] = short(s.Hash)
	}
	b.secs = SectionsOf(string(work), valid)
	return b, nil
}

// forClaim is the status cap, evidence, dependencies and tier for one accepted claim, or a
// reject code when the claim cannot be tied to a file of its component.
func (b claimBasis) forClaim(a Accepted) (status string, ev []claim.Evidence, deps []string, tier, code, why string) {
	if !b.comp {
		return CapStatus(b.src), EvidenceFor(b.src), nil, b.src.Tier, "", ""
	}
	m := locatorRe.FindStringSubmatch(a.Locator)
	start, _ := strconv.Atoi(m[1])
	end, _ := strconv.Atoi(m[2])
	sec, ok := SectionAt(b.secs, start)
	if !ok {
		return "", nil, nil, "", "locator-outside-files", "the locator does not start inside one of the component's files"
	}
	if end > sec.End {
		return "", nil, nil, "", "locator-spans-files", "a claim must come from one file; the locator crosses into the next"
	}
	return "unverified", EvidenceFor(b.files[sec.Path]), []string{sec.Path}, TierImplementation, "", ""
}

// Submit checks a proposal against its work file, admits what passes through validate.Authoring
// and stores it with the item's completion in one transaction. A submission with claims but no
// usable claim puts the item back for another try instead of completing it.
func Submit(ctx context.Context, in SubmitInput) (SubmitReport, error) {
	rep := SubmitReport{Item: Safe(in.P.Item), Rejected: []Reject{}, Findings: []string{}}
	it, err := in.Runs.Item(ctx, in.P.Item)
	if err != nil {
		return rep, err
	}
	if it.Stage != "extract" {
		return rep, fmt.Errorf("%w: item %s is a %s item", ErrState, it.ID, it.Stage)
	}
	if it.State != "leased" || it.Epoch != in.P.Epoch || it.Batch != in.Batch {
		return rep, ErrStaleLease
	}
	work, err := in.Work.Read(it.InputHash, MaxFileBytes+(64<<10))
	if err != nil {
		return rep, err
	}
	basis, err := basisFor(ctx, in.Runs, it, work)
	if err != nil {
		return rep, err
	}
	accepted, rejects := CheckProposal(in.P, work)
	rep.Rejected = append(rep.Rejected, rejects...)
	var admitted []Admitted
	for _, a := range accepted {
		status, ev, deps, tier, code, why := basis.forClaim(a)
		if code != "" {
			rep.Rejected = append(rep.Rejected, Reject{Index: a.Index, Code: code, Why: why})
			continue
		}
		ad, err := Admit(ctx, in.Env, a.ProposedClaim, status, ev, deps, tier, in.Mint)
		if err != nil {
			rep.Rejected = append(rep.Rejected, Reject{Index: a.Index, Code: "validator", Why: Safe(err.Error())})
			continue
		}
		for _, f := range ad.Findings {
			rep.Findings = append(rep.Findings, Safe(f.Msg))
		}
		admitted = append(admitted, ad)
	}
	rep.Accepted = len(admitted)
	if len(admitted) == 0 && len(in.P.Claims) > 0 {
		if err := in.Runs.FailCounted(ctx, it.ID, it.Epoch, 0, len(rep.Rejected)); err != nil {
			return rep, err
		}
		rep.Requeued = true
		return rep, nil
	}
	rejectedN := len(rep.Rejected)
	err = in.Runs.Complete(ctx, it.ID, it.Epoch, in.P.Usage, func(ctx context.Context, tx *sql.Tx, it Item) error {
		for i, a := range admitted {
			if _, err := tx.ExecContext(ctx, `INSERT INTO bs_claims(run_id,checkout_id,item_id,ordinal,claim_id,text,kind,status,meta,tier,contra)
				VALUES(?,?,?,?,?,?,?,?,?,?,'')`, it.RunID, in.Runs.CheckoutID, it.ID, i, a.Claim.ID, a.Text, a.Claim.Kind, a.Claim.Status, a.Meta, a.Tier); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE bs_runs SET accepted=accepted+?, rejected=rejected+? WHERE run_id=?`, len(admitted), rejectedN, it.RunID)
		return err
	})
	return rep, err
}
