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
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End < spans[j].End
	})
	// Two rules can match the same text (the assignment rules overlap); report each place once.
	out := spans[:0]
	for _, sp := range spans {
		if n := len(out); n > 0 && out[n-1].Start == sp.Start && out[n-1].End == sp.End {
			continue
		}
		out = append(out, sp)
	}
	return out
}
