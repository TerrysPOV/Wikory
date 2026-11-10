package bootstrap

import "testing"

// Terry's ruling (2026-11-10): structured data under a memory directory is configuration, read
// as implementation, not as a note. Only prose there is knowledge-bearing. The neighbours that
// must stay attributed are pinned beside it: prose under memory/, anything under a handoff
// directory, and a file read from a --source directory, which is never implementation. That
// last case is the way to have such a file read as a note: name its directory with --source.
func TestStructuredDataUnderMemoryIsImplementationAndItsNeighboursStayNotes(t *testing.T) {
	for p, want := range map[string]string{
		"memory/state.json":            TierImplementation,
		"memory/settings.yaml":         TierImplementation,
		"memory/settings.yml":          TierImplementation,
		".claude/memory/index.json":    TierImplementation,
		"memories/cache.yaml":          TierImplementation,
		"memory/MEMORY.md":             TierAttributed,
		"memory/notes.md":              TierAttributed,
		"memories/2026-11-01.markdown": TierAttributed,
		".claude/handoffs/state.yaml":  TierAttributed,
		"handoffs/latest.json":         TierAttributed,
		".planning/phase1/STATE.json":  TierAttributed,
	} {
		if got := TierOf(p); got != want {
			t.Errorf("TierOf(%q) = %q, want %q", p, got, want)
		}
	}
	for _, p := range []string{"state.json", "memory/state.json", "settings.yaml"} {
		if got := outsideTier(p); got != TierAttributed {
			t.Errorf("a file read through --source is never implementation: outsideTier(%q) = %q", p, got)
		}
	}
}
