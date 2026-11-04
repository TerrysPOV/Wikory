package bootstrap

import (
	"path"
	"strings"
)

var implExt = map[string]bool{".go": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true,
	".tsx": true, ".jsx": true, ".rs": true, ".java": true, ".kt": true, ".rb": true, ".php": true, ".c": true,
	".h": true, ".cc": true, ".cpp": true, ".cs": true, ".swift": true, ".sh": true, ".sql": true, ".svelte": true,
	".vue": true, ".toml": true, ".yaml": true, ".yml": true, ".json": true, ".mod": true, ".gradle": true}

var implBase = map[string]bool{"makefile": true, "dockerfile": true}

// Handoffs, planning notes and memory notes are attributed claims. They are recognised by whole
// path segments and base names, never by substring, so code that merely has such a word in its
// path (internal/memory/store.go, pkg/inmemory/cache.go) stays code.
var (
	// handoffDirs mark everything beneath them, whatever the extension.
	handoffDirs = map[string]bool{"handoffs": true, ".planning": true}
	// noteDirs mark only prose beneath them: a directory called memory or handoff is as likely to
	// be a Go package as a folder of notes.
	noteDirs = map[string]bool{"handoff": true, "memory": true, "memories": true}
	// handoffBases are file names that are notes wherever they sit.
	handoffBases = map[string]bool{"memory.md": true, "agents.md": true, "claude.md": true}
	proseExt     = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".rst": true}
)

// IsHandoff reports whether a path looks like a handoff, planning note or memory note: the
// material whose mentions of a component say people were working on it.
func IsHandoff(p string) bool {
	segs := strings.Split(strings.ToLower(p), "/")
	base := segs[len(segs)-1]
	if handoffBases[base] {
		return true
	}
	prose := proseExt[path.Ext(base)]
	if prose && (strings.HasPrefix(base, "handoff") || strings.HasPrefix(base, "prior_session_state")) {
		return true
	}
	for _, s := range segs[:len(segs)-1] {
		if handoffDirs[s] || prose && noteDirs[s] {
			return true
		}
	}
	return false
}

func isDecision(l string) bool {
	if !strings.HasSuffix(l, ".md") {
		return false
	}
	for _, seg := range strings.Split(path.Dir(l), "/") {
		if seg == "adr" || seg == "adrs" || seg == "decisions" {
			return true
		}
	}
	return strings.HasPrefix(path.Base(l), "adr-")
}

// TierOf is the tier of a file in the repository. Handoffs are checked first, so a JSON file
// under .planning/ is a note, not configuration.
func TierOf(p string) string {
	l := strings.ToLower(p)
	switch {
	case IsHandoff(l):
		return TierAttributed
	case isDecision(l):
		return TierIntent
	case implExt[path.Ext(l)] || implBase[path.Base(l)]:
		return TierImplementation
	}
	return TierAttributed
}

// outsideTier is the tier of a file read from an opted-in directory outside the repository's
// tracked tree: never implementation, because it is not this project's code.
func outsideTier(p string) string {
	if isDecision(strings.ToLower(p)) {
		return TierIntent
	}
	return TierAttributed
}
