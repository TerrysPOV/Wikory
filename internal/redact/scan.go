package redact

import "sort"

// Span is one byte range of the input that a rule matched. For a rule with a "secret" group it
// is that group only, so the key name and the separator are outside it.
type Span struct {
	Kind       string
	Start, End int
}

// Scan reports where each rule matches the original text, ordered by position. It never
// rewrites anything and never returns the matched text: callers record the kind and compute a
// line number, so a coverage report can say where a secret was without repeating it.
//
// Overlapping or nested spans are merged into the widest (the assignment rules overlap, and
// one secret must be one span), so the result is ordered by Start and no two spans overlap.
// A merged span keeps the Kind of the span that starts first.
//
// Scan and String disagree about input that already contains a marker: String rewrites
// "[REDACTED:" to "[REDACTED-IN:" before any rule runs, whereas Scan trusts a value that is
// exactly one marker and skips it. Scan is a detector over the original text, not a mirror.
func Scan(s string) []Span {
	var spans []Span
	for _, r := range rules {
		secret, cmp := r.re.SubexpIndex("secret"), r.re.SubexpIndex("cmp")
		for _, loc := range r.re.FindAllStringSubmatchIndex(s, -1) {
			if cmp >= 0 && loc[2*cmp] >= 0 {
				continue // a comparison, not an assignment
			}
			start, end := loc[0], loc[1]
			if secret >= 0 && loc[2*secret] >= 0 {
				start, end = loc[2*secret], loc[2*secret+1]
			}
			if end <= start || markerRe.MatchString(s[start:end]) {
				continue
			}
			spans = append(spans, Span{Kind: r.kind, Start: start, End: end})
		}
	}
	// Position order, widest first at equal Start. The widest-first tie-break is an
	// optimisation, not a correctness requirement: it makes same-Start nesting cheap (the
	// narrower span is skipped as nested) but the merge below unions any overlap either way.
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End > spans[j].End
	})
	out := spans[:0]
	for _, sp := range spans {
		if n := len(out); n > 0 && sp.Start < out[n-1].End {
			if sp.End > out[n-1].End {
				out[n-1].End = sp.End
			}
			continue
		}
		out = append(out, sp)
	}
	return out
}
