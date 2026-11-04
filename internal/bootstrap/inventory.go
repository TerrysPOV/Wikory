package bootstrap

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/redact"
)

// Options tune one inventory.
type Options struct {
	MaxFileBytes int64
	// Churn is commits per path over the churn window, for the repository reader's paths.
	Churn map[string]int
	// LastChanged is the newest commit time per path, for the repository reader's paths. It
	// becomes Source.Date; it never changes what is done, so it stays out of the plan hash.
	LastChanged map[string]time.Time
	// Unread are sources that exist but are deliberately not read (other branches and
	// worktrees). They are carried into the result as they are.
	Unread []Source
	Budget int
	// Progress, when set, is called with (done, total) as files are read.
	Progress func(done, total int)
}

type listed struct {
	r Reader
	e Entry
	s Source
}

// Inventory lists every reader, drops what must not be read, scans what is left for secrets,
// hashes it, derives components from the implementation-tier paths and counts how often docs
// and handoffs mention each one. File text is held only while it is being examined. The result
// carries hashes, sizes, dispositions and secret line numbers, never text.
func Inventory(ctx context.Context, readers []Reader, opt Options) (Result, error) {
	if opt.MaxFileBytes <= 0 {
		opt.MaxFileBytes = MaxFileBytes
	}
	var all []listed
	for _, r := range readers {
		es, err := r.List(ctx)
		if err != nil {
			return Result{}, err
		}
		for _, e := range es {
			date := dateOf(e.Modified)
			if date == "" && e.Source == "repo" {
				date = dateOf(opt.LastChanged[e.Path])
			}
			all = append(all, listed{r: r, e: e, s: Source{Name: e.Source, Path: e.Path, OID: e.OID, Size: e.Size, Tier: r.Tier(e.Path), Date: date}})
		}
		// Whatever the reader dropped without listing becomes a source with that disposition and
		// no content, so it shows up in the plan and the coverage report instead of vanishing.
		if sk, ok := r.(Skipper); ok {
			for _, dropped := range sk.Skipped() {
				all = append(all, listed{r: r, e: Entry{Source: r.Name(), Path: dropped.Path},
					s: Source{Name: r.Name(), Path: dropped.Path, Tier: r.Tier(dropped.Path), Disposition: dropped.Reason}})
			}
		}
	}
	for _, u := range opt.Unread {
		all = append(all, listed{s: u})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].s.Name != all[j].s.Name {
			return all[i].s.Name < all[j].s.Name
		}
		return all[i].s.Path < all[j].s.Path
	})

	var implPaths []string
	for i := range all {
		s := &all[i].s
		if s.Disposition != "" {
			continue // a reader already recorded why this was not read
		}
		if why := Exclusion(s.Path, s.Size, nil); why != "" {
			s.Disposition = why
		} else if s.Tier == TierImplementation && s.Name == "repo" {
			implPaths = append(implPaths, s.Path)
		}
	}
	comps := DetectComponents(implPaths)
	prefixes := map[string]bool{}
	for _, c := range comps {
		prefixes[c.Prefix] = true
	}
	idx := map[string]*Component{}
	for i := range comps {
		idx[comps[i].Prefix] = &comps[i]
	}
	for p, n := range opt.Churn {
		if c := idx[ownerOf(p, prefixes)]; c != nil {
			c.Churn += n
		}
	}

	for i := range all {
		s := &all[i].s
		if opt.Progress != nil {
			opt.Progress(i, len(all))
		}
		if s.Disposition != "" {
			continue
		}
		data, err := all[i].r.Read(ctx, all[i].e, opt.MaxFileBytes)
		if err != nil {
			s.Disposition = "skipped:unreadable"
			continue
		}
		if why := Exclusion(s.Path, int64(len(data)), data); why != "" {
			s.Disposition = why
			continue
		}
		text := string(data)
		if spans := redact.Scan(text); len(spans) > 0 {
			s.Disposition, s.SecretSpans = "skipped:secret", len(spans)
			for _, sp := range spans {
				s.SecretLines = append(s.SecretLines, 1+strings.Count(text[:sp.Start], "\n"))
			}
			continue
		}
		s.Hash, s.Disposition = ids.SHA256Hex(data), DispQueued
		if s.Tier != TierImplementation {
			countMentions(comps, s.Path, text)
		}
	}
	if opt.Progress != nil {
		opt.Progress(len(all), len(all))
	}

	res := Result{Components: Rank(comps, opt.Budget)}
	for _, l := range all {
		res.Sources = append(res.Sources, l.s)
	}
	return res, nil
}

// dateOf formats a time the way Source.Date holds it: RFC3339 in UTC, or "" for the zero time.
func dateOf(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// countMentions adds one to each component the text names by path. A handoff counts toward
// HandoffMentions and any other non-code file toward DocRefs. A component is named at most
// once per file, so a file that repeats a path cannot inflate its score.
func countMentions(comps []Component, p, text string) {
	handoff := IsHandoff(p)
	for i := range comps {
		pre := comps[i].Prefix
		if pre == "." || !(strings.Contains(text, pre+"/") || (strings.Contains(pre, "/") && strings.Contains(text, pre))) {
			continue
		}
		if handoff {
			comps[i].HandoffMentions++
		} else {
			comps[i].DocRefs++
		}
	}
}
