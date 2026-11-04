package bootstrap

import (
	"encoding/json"
	"sort"

	"github.com/TerrysPOV/Wikory/internal/ids"
)

// Initial constants. The sample run replaces them with measurements (architecture spec 14.2);
// until then every figure derived from them is labelled an estimate.
const (
	BatchSize            = 8
	DigestMaxBytes       = 60 << 10
	bytesPerToken        = 4
	promptOverheadTokens = 400
	extractOutPerItem    = 700
	verifyInTokens       = 6000
	verifyOutTokens      = 800
)

// ItemSpec describes one unit of model work before it has an id.
type ItemSpec struct {
	Stage, Ref, InputHash string
	Bytes                 int64
}

// ExtractSpecs lists the extract items for a result: intent-tier sources, then attributed
// ones, then one digest per inspected component in rank order. The order is part of the plan.
func ExtractSpecs(res Result) []ItemSpec {
	var specs []ItemSpec
	for _, tier := range []string{TierIntent, TierAttributed} {
		for _, s := range res.Sources {
			if s.Disposition == DispQueued && s.Tier == tier {
				specs = append(specs, ItemSpec{Stage: "extract", Ref: "src:" + s.Name + ":" + s.Path, InputHash: s.Hash, Bytes: s.Size})
			}
		}
	}
	files := ComponentFiles(res)
	for _, c := range res.Components {
		if !c.Inspected || len(files[c.Prefix]) == 0 {
			continue // a component with nothing to read gets no paid item
		}
		var total int64
		parts := make([]string, 0, len(files[c.Prefix]))
		for _, f := range files[c.Prefix] {
			total += f.Size + int64(headerBytes(f.Path))
			parts = append(parts, f.Path+":"+f.Hash)
		}
		sort.Strings(parts)
		if total > DigestMaxBytes {
			total = DigestMaxBytes
		}
		specs = append(specs, ItemSpec{Stage: "extract", Ref: "comp:" + c.Prefix, InputHash: ids.Digest(parts...), Bytes: total})
	}
	return specs
}

// SampleSet picks n of total items spread evenly, by index. n <= 0 picks none; n >= total all.
func SampleSet(n, total int) map[int]bool {
	set := map[int]bool{}
	switch {
	case n <= 0:
	case n >= total:
		for i := 0; i < total; i++ {
			set[i] = true
		}
	default:
		for i := 0; i < n; i++ {
			set[i*total/n] = true
		}
	}
	return set
}

// Estimate is the expected size of one stage.
type Estimate struct {
	Stage        string `json:"stage"`
	Items        int    `json:"items"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// EstimateStages sizes extract and verify. Extract tokens come from each item's byte count:
// bytes divided by bytesPerToken plus a fixed prompt overhead, with a fixed output per item.
// Verify uses fixed per-item constants and does not look at byte counts.
func EstimateStages(res Result) []Estimate {
	specs := ExtractSpecs(res)
	ex := Estimate{Stage: "extract", Items: len(specs), OutputTokens: len(specs) * extractOutPerItem}
	for _, s := range specs {
		ex.InputTokens += int(s.Bytes)/bytesPerToken + promptOverheadTokens
	}
	n := 0
	files := ComponentFiles(res)
	for _, c := range res.Components {
		if c.Inspected && len(files[c.Prefix]) > 0 {
			n++
		}
	}
	return []Estimate{ex, {Stage: "verify", Items: n, InputTokens: n * verifyInTokens, OutputTokens: n * verifyOutTokens}}
}

// Plan is a bootstrap plan: what would be read, what it would cost and how it is grouped.
type Plan struct {
	Scope, Head, Hash string
	Sample, Batches   int
	Roots             map[string]string // --source name -> directory; not part of the hash
	Result
	Estimates []Estimate
}

type hashSource struct{ Name, Path, Hash, Tier, Disposition string }
type hashComponent struct {
	Prefix    string
	Rank      int
	Inspected bool
}

// BuildPlan assembles the plan and its hash. The hash is built from the work and the numbers
// Terry approves, not only from the inputs, so a change to anything that would change what is
// done or what it is said to cost changes the hash. See planHash. It does not cover the sample
// size or the Roots.
func BuildPlan(res Result, scope, head string, sample int) Plan {
	est := EstimateStages(res)
	p := Plan{Scope: scope, Head: head, Sample: sample, Result: res, Estimates: est}
	for _, e := range est {
		p.Batches += (e.Items + BatchSize - 1) / BatchSize
	}
	p.Hash = planHash(p)
	return p
}

// planHash hashes the plan's inputs (scope, HEAD, weights, limits, every source's content hash,
// tier and disposition, the component ranking) together with what they produce: the ordered
// extract work list and the estimates and batch count Terry sees. A changed estimate constant
// or tier classifier therefore changes the hash even when the inputs do not.
//
// Only what is identical for the same repository state on another machine is hashed: the
// records of other branches and worktrees (local branch names, how far ahead they are, the
// branch the default is taken from) are left out. They are still stored and reported.
func planHash(p Plan) string {
	h := struct {
		V          int
		Scope      string
		Head       string
		Weights    [4]float64
		Constants  [4]int
		Sources    []hashSource
		Components []hashComponent
		Work       []ItemSpec
		Estimates  []Estimate
		Batches    int
	}{V: 3, Scope: p.Scope, Head: p.Head,
		Weights:   [4]float64{WeightChurn, WeightDocRef, WeightHandoff, WeightEntry},
		Constants: [4]int{BatchSize, DigestMaxBytes, InspectBudget, CapChurn},
		Work:      ExtractSpecs(p.Result), Estimates: p.Estimates, Batches: p.Batches}
	for _, s := range p.Sources {
		if unread(s) {
			continue
		}
		h.Sources = append(h.Sources, hashSource{s.Name, s.Path, s.Hash, s.Tier, s.Disposition})
	}
	for _, c := range p.Components {
		h.Components = append(h.Components, hashComponent{c.Prefix, c.Rank, c.Inspected})
	}
	b, err := json.Marshal(h)
	if err != nil {
		panic("bootstrap: plan hash input cannot be marshalled: " + err.Error()) // plain strings and numbers: cannot happen
	}
	return ids.SHA256Hex(b)
}
