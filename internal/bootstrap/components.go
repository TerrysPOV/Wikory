package bootstrap

import (
	"path"
	"sort"
	"strings"
)

// DetectComponents groups implementation-tier file paths into components. A component is the
// directory of a Go package (a directory holding .go files) or of a package.json; any other
// file belongs to the nearest enclosing package directory, or else to its top-level
// directory. A file at the repository root belongs to ".". Components come back sorted by prefix.
func DetectComponents(paths []string) []Component {
	pkg := map[string]bool{}
	for _, p := range paths {
		dir, base := path.Dir(p), path.Base(p)
		if (strings.HasSuffix(base, ".go") || base == "package.json") && !hasSegment(dir, "testdata") {
			pkg[dir] = true
		}
	}
	by := map[string]*Component{}
	for _, p := range paths {
		prefix := owner(p, pkg)
		c := by[prefix]
		if c == nil {
			c = &Component{Name: prefix, Prefix: prefix}
			by[prefix] = c
		}
		c.Files++
		if path.Base(p) == "main.go" || strings.HasPrefix(prefix, "cmd/") {
			c.Entry = true
		}
	}
	out := make([]Component, 0, len(by))
	for _, c := range by {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prefix < out[j].Prefix })
	return out
}

func hasSegment(dir, seg string) bool {
	for _, s := range strings.Split(dir, "/") {
		if s == seg {
			return true
		}
	}
	return false
}

// owner is the component prefix for one file: the longest package directory that contains it,
// else its top-level directory, else ".".
func owner(p string, pkg map[string]bool) string {
	dir := path.Dir(p)
	for d := dir; d != "." && d != "/"; d = path.Dir(d) {
		if pkg[d] {
			return d
		}
	}
	if dir == "." {
		return "."
	}
	top, _, _ := strings.Cut(p, "/")
	return top
}

// ownerOf returns the prefix that owns p among the detected components, for attaching churn.
func ownerOf(p string, prefixes map[string]bool) string {
	for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
		if prefixes[d] {
			return d
		}
	}
	top, _, _ := strings.Cut(p, "/")
	if prefixes[top] {
		return top
	}
	return "."
}

// ComponentFiles maps each component prefix to the queued implementation-tier repository
// files it owns, in path order.
func ComponentFiles(res Result) map[string][]Source {
	prefixes := map[string]bool{}
	for _, c := range res.Components {
		prefixes[c.Prefix] = true
	}
	out := map[string][]Source{}
	for _, s := range res.Sources {
		if s.Name != "repo" || s.Tier != TierImplementation || s.Disposition != DispQueued {
			continue
		}
		pre := ownerOf(s.Path, prefixes)
		out[pre] = append(out[pre], s)
	}
	return out
}
