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

var handoffMarks = []string{"handoff", "prior_session_state", ".planning/", "/memory/", "memory/", "memory.md", "agents.md", "claude.md"}

// IsHandoff reports whether a path looks like a handoff, planning note or memory note: the
// material whose mentions of a component say people were working on it.
func IsHandoff(p string) bool {
	l := strings.ToLower(p)
	for _, m := range handoffMarks {
		if strings.Contains(l, m) {
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
