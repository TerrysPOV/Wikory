package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/validate"
)

// Verdict results.
const (
	ResultConfirmed       = "confirmed"
	ResultContradicted    = "contradicted"
	ResultNotDeterminable = "not-determinable"
)

const maxEvidencePerVerdict = 5

var linesRe = regexp.MustCompile(`^L([1-9][0-9]{0,6})-L([1-9][0-9]{0,6})$`)

// errClaimMoved means a claim changed between the moment a verdict was judged and the moment it
// was written, so the write was refused.
var errClaimMoved = errors.New("bootstrap: a claim was decided by another verdict while this one was being checked")

// Locator names lines of one repository file.
type Locator struct {
	Path  string `json:"path"`
	Lines string `json:"lines"`
}

// Verdict is a verifier's answer for one claim.
type Verdict struct {
	Claim     string    `json:"claim"`
	Result    string    `json:"result"`
	Evidence  []Locator `json:"evidence"`
	Corrected string    `json:"corrected"`
}

// VerdictSet is a verifier's answer for one verify item.
type VerdictSet struct {
	Item     string    `json:"item"`
	Epoch    int       `json:"epoch"`
	Verdicts []Verdict `json:"verdicts"`
	Usage    Usage     `json:"usage"`
}

// VerifyInput is one verify submission. Blob reads a blob by its object id: the line ranges in
// the verdicts are held to the length of the files it returns, and with no Blob no range is
// accepted.
type VerifyInput struct {
	Runs  *Runs
	Env   validate.Env
	Mint  func() string
	Blob  func(oid string) ([]byte, error)
	Batch string
	V     VerdictSet
}

// VerifyReport says what became of one verify submission.
type VerifyReport struct {
	Item         string   `json:"item"`
	Confirmed    int      `json:"confirmed"`
	Contradicted int      `json:"contradicted"`
	Undetermined int      `json:"undetermined"`
	Ignored      int      `json:"ignored"`
	Rejected     []Reject `json:"rejected"`
	Findings     []string `json:"findings"`
	Requeued     bool     `json:"requeued"`
}

// ClaimsFor returns the claims a component's verifier could judge: those not yet decided
// that came from the component's digest or name it by path. The root component "." matches by
// digest only, because "." appears in ordinary text. A claim can match several components;
// AssignClaims decides which one judges it.
func ClaimsFor(c Component, claims []StoredClaim) []StoredClaim {
	var out []StoredClaim
	for _, s := range claims {
		if s.Status != "unverified" && s.Status != "historical" {
			continue
		}
		if s.Ref == "comp:"+c.Prefix || (c.Prefix != "." && strings.Contains(s.Text, c.Prefix)) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ClaimID < out[j].ClaimID })
	return out
}

// AssignClaims gives every undecided claim to exactly one inspected component, keyed by the
// component's prefix. A claim that names two components is therefore judged once, and two
// verifiers can never race over it. A claim that came from a component's own digest belongs to
// that component; any other goes to the component whose path it names most specifically (the
// longest prefix), then the better rank, then the lower name. Components that are not inspected
// receive nothing.
func AssignClaims(comps []Component, claims []StoredClaim) map[string][]StoredClaim {
	var inspected []Component
	for _, c := range comps {
		if c.Inspected {
			inspected = append(inspected, c)
		}
	}
	out := map[string][]StoredClaim{}
	for _, s := range claims {
		var best Component
		found := false
		for _, c := range inspected {
			if len(ClaimsFor(c, []StoredClaim{s})) == 0 {
				continue
			}
			if !found || ownsBetter(c, best, s) {
				best, found = c, true
			}
		}
		if found {
			out[best.Prefix] = append(out[best.Prefix], s)
		}
	}
	for k := range out {
		list := out[k]
		sort.SliceStable(list, func(i, j int) bool { return list[i].ClaimID < list[j].ClaimID })
	}
	return out
}

// ownsBetter says whether a is a better owner of s than b is.
func ownsBetter(a, b Component, s StoredClaim) bool {
	if ao, bo := s.Ref == "comp:"+a.Prefix, s.Ref == "comp:"+b.Prefix; ao != bo {
		return ao
	}
	if len(a.Prefix) != len(b.Prefix) {
		return len(a.Prefix) > len(b.Prefix)
	}
	if a.Rank != b.Rank {
		return a.Rank < b.Rank
	}
	return a.Name < b.Name
}

// EnqueueVerify adds verify items to a run. A component that already has one is left alone, so
// two callers racing to prepare the same run queue one paid verification per component.
func (r *Runs) EnqueueVerify(ctx context.Context, runID string, specs []ItemSpec) error {
	return r.write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, sp := range specs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO bs_items(item_id,run_id,checkout_id,stage,ref,input_hash,state) VALUES(?,?,?,'verify',?,?,'pending')
				ON CONFLICT(run_id, stage, ref) DO NOTHING`,
				r.NewID("itm_"), runID, r.CheckoutID, sp.Ref, sp.InputHash); err != nil {
				return err
			}
		}
		return nil
	})
}

// PrepareVerify creates the verify items once every extract item is closed: one per inspected
// component that has claims to judge, each with a work file listing the component's files and
// those claims. It does nothing while extraction is open, and nothing when verify items exist.
func PrepareVerify(ctx context.Context, runs *Runs, w WorkFiles, runID string) (int, error) {
	st, err := runs.Status(ctx, runID)
	if err != nil {
		return 0, err
	}
	if st.Run.State != StateRunning || len(st.Items["verify"]) > 0 || st.Items["extract"]["pending"]+st.Items["extract"]["leased"] > 0 {
		return 0, nil
	}
	comps, err := runs.Components(ctx, runID)
	if err != nil {
		return 0, err
	}
	srcs, err := runs.Sources(ctx, runID)
	if err != nil {
		return 0, err
	}
	claims, err := runs.Claims(ctx, runID)
	if err != nil {
		return 0, err
	}
	files := ComponentFiles(Result{Sources: srcs, Components: comps})
	assigned := AssignClaims(comps, claims)
	var specs []ItemSpec
	for _, c := range comps {
		if !c.Inspected {
			continue
		}
		related := assigned[c.Prefix]
		if len(related) == 0 {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "COMPONENT %s\nFILES\n", c.Prefix)
		for _, f := range files[c.Prefix] {
			b.WriteString(f.Path + "\n")
		}
		b.WriteString("CLAIMS\n")
		for _, s := range related {
			fmt.Fprintf(&b, "--- claim %s kind=%s\n%s\n", s.ClaimID, s.Kind, s.Text)
		}
		hash := ids.SHA256Hex([]byte(b.String()))
		if _, err := w.Write(hash, b.String()); err != nil {
			return 0, err
		}
		specs = append(specs, ItemSpec{Stage: "verify", Ref: "comp:" + c.Prefix, InputHash: hash})
	}
	return len(specs), runs.EnqueueVerify(ctx, runID, specs)
}

// quietReport is what an infrastructure failure reports: the item the store found and nothing
// else, because nothing was recorded and a retry would count any earlier reject again.
func quietReport(item string) VerifyReport {
	return VerifyReport{Item: item, Rejected: []Reject{}, Findings: []string{}}
}

// change is one write a verify submission makes. An update carries the claim as it was read
// (before): the write is refused if the row is no longer that.
type change struct {
	update *StoredClaim // existing claim to rewrite (meta, status, contra, loc)
	before StoredClaim  // that claim as it was read
	insert *StoredClaim // new claim to add
}

// ApplyVerdicts checks a verifier's verdicts and applies them in one transaction with the
// item's completion. A verdict is applied only to a claim assigned to this item's component that
// is still undecided. Evidence must be a code, configuration or test file of this component,
// queued in this run, with a line range that ends inside the file.
func ApplyVerdicts(ctx context.Context, in VerifyInput) (VerifyReport, error) {
	rep := VerifyReport{Rejected: []Reject{}, Findings: []string{}}
	if err := in.V.Usage.Check(); err != nil {
		return rep, err
	}
	// The item id in a verdict set is chosen by the verifier, so the report names an item only
	// once the store has found it, by the store's own id.
	it, err := in.Runs.Item(ctx, in.V.Item)
	if err != nil {
		return rep, err
	}
	rep.Item = it.ID
	if it.Stage != "verify" {
		return rep, fmt.Errorf("%w: item %s is a %s item", ErrState, it.ID, it.Stage)
	}
	if it.State != "leased" || it.Epoch != in.V.Epoch || it.Batch != in.Batch {
		return rep, ErrStaleLease
	}
	comps, err := in.Runs.Components(ctx, it.RunID)
	if err != nil {
		return rep, err
	}
	prefix := strings.TrimPrefix(it.Ref, "comp:")
	var comp Component
	found := false
	for _, c := range comps {
		if c.Prefix == prefix {
			comp, found = c, true
		}
	}
	if !found {
		return rep, fmt.Errorf("bootstrap: item %s names a component that is not in the run", it.ID)
	}
	srcs, err := in.Runs.Sources(ctx, it.RunID)
	if err != nil {
		return rep, err
	}
	owned := map[string]Source{}
	for _, s := range ComponentFiles(Result{Sources: srcs, Components: comps})[comp.Prefix] {
		owned[s.Path] = s
	}
	all, err := in.Runs.Claims(ctx, it.RunID)
	if err != nil {
		return rep, err
	}
	byID := map[string]StoredClaim{}
	for _, s := range all {
		byID[s.ClaimID] = s
	}
	judge := map[string]bool{}
	for _, s := range AssignClaims(comps, all)[comp.Prefix] {
		judge[s.ClaimID] = true
	}

	var changes []change
	seen := map[string]bool{}
	decided := map[string]bool{}
	for i, v := range in.V.Verdicts {
		rej := func(c, why string) { rep.Rejected = append(rep.Rejected, Reject{Index: i, Code: c, Why: why}) }
		cur, known := byID[v.Claim]
		switch {
		case !known:
			rej("unknown-claim", "no such claim in this run")
			continue
		case seen[v.Claim]:
			rej("duplicate", "two verdicts for one claim")
			continue
		case cur.Status != "unverified" && cur.Status != "historical":
			seen[v.Claim] = true
			rep.Ignored++
			continue
		case !judge[v.Claim]:
			rej("unknown-claim", "this claim is not part of this component's work file")
			continue
		}
		seen[v.Claim] = true
		if v.Result == ResultNotDeterminable {
			decided[v.Claim] = true
			rep.Undetermined++
			continue
		}
		if v.Result != ResultConfirmed && v.Result != ResultContradicted {
			rej("bad-result", "result must be confirmed, contradicted or not-determinable")
			continue
		}
		if len(v.Evidence) == 0 {
			rej("no-evidence", "a confirmed or contradicted verdict needs code evidence")
			continue
		}
		if len(v.Evidence) > maxEvidencePerVerdict {
			rej("no-evidence", "at most five evidence locations per verdict")
			continue
		}
		var paths []string
		var locs []Locator
		okEv := true
		for ei, l := range v.Evidence {
			src, mine := owned[l.Path]
			if _, err := confine.Split(l.Path); err != nil || !mine {
				rej("bad-evidence-path", "evidence item "+strconv.Itoa(ei)+" is not a code, configuration or test file of this component")
				okEv = false
				break
			}
			if why := checkLines(in.Blob, src, l.Lines); why != "" {
				rej("bad-evidence-lines", "evidence item "+strconv.Itoa(ei)+": "+why)
				okEv = false
				break
			}
			paths = append(paths, l.Path)
			locs = append(locs, l)
		}
		if !okEv {
			continue
		}
		parsed, err := claim.ParseComment(cur.Meta)
		if err != nil {
			rej("validator", "the stored claim no longer parses: "+Safe(err.Error()))
			continue
		}
		if v.Result == ResultContradicted {
			if c, why := CheckText(v.Corrected); c != "" {
				rej(c, "the corrected claim: "+why)
				continue
			}
			repl := claim.Claim{Kind: "current-impl", Status: "conflicting", Evidence: blobEvidence(paths), Deps: uniq(paths), Author: "agent", Contradicts: []string{cur.ClaimID}}
			outNew, f1, err := validate.Authoring(ctx, repl, v.Corrected, in.Env, in.Mint)
			if err != nil {
				if !IsRefusal(err) {
					return quietReport(it.ID), err
				}
				rej("validator", Safe(err.Error()))
				continue
			}
			parsed.Status = "conflicting"
			parsed.Contradicts = append(parsed.Contradicts, outNew.ID)
			outOld, f2, err := validate.Authoring(ctx, parsed, cur.Text, in.Env, in.Mint)
			if err != nil {
				if !IsRefusal(err) {
					return quietReport(it.ID), err
				}
				rej("validator", Safe(err.Error()))
				continue
			}
			for _, f := range append(f1, f2...) {
				rep.Findings = append(rep.Findings, Safe(f.Msg))
			}
			upd := cur
			upd.Status, upd.Meta, upd.Loc = outOld.Status, outOld.Render(), locJSON(locs)
			ins := StoredClaim{ClaimID: outNew.ID, Text: v.Corrected, Kind: "current-impl", Status: outNew.Status, Meta: outNew.Render(), Tier: TierImplementation, Loc: locJSON(locs)}
			changes = append(changes, change{update: &upd, before: cur}, change{insert: &ins})
			decided[v.Claim] = true
			rep.Contradicted++
			continue
		}
		parsed.Status = "verified"
		parsed.Evidence = mergeEvidence(parsed.Evidence, blobEvidence(paths))
		parsed.Deps = uniq(append(append([]string{}, parsed.Deps...), paths...))
		out, fs, err := validate.Authoring(ctx, parsed, cur.Text, in.Env, in.Mint)
		if err != nil {
			if !IsRefusal(err) {
				return quietReport(it.ID), err
			}
			rej("validator", Safe(err.Error()))
			continue
		}
		for _, f := range fs {
			rep.Findings = append(rep.Findings, Safe(f.Msg))
		}
		upd := cur
		upd.Status, upd.Meta, upd.Loc = out.Status, out.Render(), locJSON(locs)
		changes = append(changes, change{update: &upd, before: cur})
		decided[v.Claim] = true
		if out.Status == "verified" {
			rep.Confirmed++
		} else {
			rep.Undetermined++
		}
	}
	for id := range judge {
		if !decided[id] && !seen[id] {
			rep.Undetermined++
		}
	}
	if len(in.V.Verdicts) > 0 && len(changes) == 0 && rep.Undetermined == 0 && rep.Ignored == 0 {
		if err := in.Runs.FailCounted(ctx, it.ID, it.Epoch, 0, 0, in.V.Usage); err != nil {
			return rep, err
		}
		rep.Requeued = true
		return rep, nil
	}
	err = in.Runs.Complete(ctx, it.ID, it.Epoch, in.V.Usage, func(ctx context.Context, tx *sql.Tx, it Item) error {
		return writeChanges(ctx, tx, in.Runs.CheckoutID, it, changes)
	})
	if errors.Is(err, errClaimMoved) {
		// Another verdict decided one of these claims after they were read. Nothing was written
		// (the transaction rolled back); the item goes back for a fresh look at the claims as
		// they are now.
		rep.Confirmed, rep.Contradicted, rep.Undetermined = 0, 0, 0
		if ferr := in.Runs.FailCounted(ctx, it.ID, it.Epoch, 0, 0, in.V.Usage); ferr != nil {
			return rep, ferr
		}
		rep.Requeued = true
		return rep, nil
	}
	return rep, err
}

// writeChanges applies a verify submission's changes inside the completion transaction. An
// update is a compare-and-swap: it matches only a row still holding the status and metadata the
// verdict was judged against, so a later or concurrent verdict can never be overwritten.
func writeChanges(ctx context.Context, tx *sql.Tx, checkoutID string, it Item, changes []change) error {
	var maxOrd sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT max(ordinal) FROM bs_claims WHERE item_id=?`, it.ID).Scan(&maxOrd); err != nil {
		return err
	}
	ord := int(maxOrd.Int64)
	if maxOrd.Valid {
		ord++
	}
	for _, ch := range changes {
		switch {
		case ch.update != nil:
			u := ch.update
			contra := ""
			if p, err := claim.ParseComment(u.Meta); err == nil {
				contra = strings.Join(p.Contradicts, ",")
			}
			res, err := tx.ExecContext(ctx, `UPDATE bs_claims SET meta=?, status=?, contra=?, loc=? WHERE item_id=? AND ordinal=? AND claim_id=? AND status=? AND meta=?`,
				u.Meta, u.Status, contra, u.Loc, u.Item, u.Ordinal, u.ClaimID, ch.before.Status, ch.before.Meta)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return errClaimMoved
			}
		case ch.insert != nil:
			n := ch.insert
			contra := ""
			if p, err := claim.ParseComment(n.Meta); err == nil {
				contra = strings.Join(p.Contradicts, ",")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO bs_claims(run_id,checkout_id,item_id,ordinal,claim_id,text,kind,status,meta,tier,contra,loc)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, it.RunID, checkoutID, it.ID, ord, n.ClaimID, n.Text, n.Kind, n.Status, n.Meta, n.Tier, contra, n.Loc); err != nil {
				return err
			}
			ord++
		}
	}
	return nil
}

// checkLines holds a verdict's line range to the file it cites: it must look like L10-L20, run
// forwards, and end inside the file the plan recorded. With no blob reader nothing can be
// bounded, so nothing is accepted. The reason it returns never repeats the range it was given.
func checkLines(blob func(oid string) ([]byte, error), src Source, lines string) string {
	m := linesRe.FindStringSubmatch(lines)
	if m == nil {
		return "lines must look like L10-L20"
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	if a > b {
		return "the line range runs backwards"
	}
	if blob == nil {
		return "the file's length cannot be checked"
	}
	data, err := blob(src.OID)
	if err != nil {
		return "the file could not be read to check the range"
	}
	n := strings.Count(string(data), "\n")
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	if b > n {
		return "the range ends beyond the end of the file"
	}
	return ""
}

// locJSON keeps a verdict's evidence locators privately with the claim they decided. The claim
// grammar has no line field, so the lines live only in the run's table.
func locJSON(locs []Locator) string {
	b, err := json.Marshal(locs)
	if err != nil {
		return ""
	}
	return string(b)
}

func blobEvidence(paths []string) []claim.Evidence {
	var ev []claim.Evidence
	for _, p := range uniq(paths) {
		ev = append(ev, claim.Evidence{Kind: claim.EvBlob, Path: p})
	}
	return ev
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// mergeEvidence keeps existing evidence and adds blob evidence for paths not already named.
func mergeEvidence(have, add []claim.Evidence) []claim.Evidence {
	out := append([]claim.Evidence(nil), have...)
	named := map[string]bool{}
	for _, e := range have {
		if e.Kind == claim.EvBlob {
			named[e.Path] = true
		}
	}
	for _, e := range add {
		if !named[e.Path] {
			out = append(out, e)
		}
	}
	return out
}
