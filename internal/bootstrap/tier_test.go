package bootstrap

import "testing"

func TestTierOf(t *testing.T) {
	cases := map[string]string{
		"internal/store/schema.go":                TierImplementation,
		"internal/store/schema_test.go":           TierImplementation,
		"config.toml":                             TierImplementation,
		"Makefile":                                TierImplementation,
		"docs/decisions/0001-use-sqlite.md":       TierIntent,
		"docs/adr/ADR-0002-queues.md":             TierIntent,
		"README.md":                               TierAttributed,
		".claude/handoffs/PRIOR_SESSION_STATE.md": TierAttributed,
		".planning/notes.json":                    TierAttributed,
		"memory/MEMORY.md":                        TierAttributed,
		"notes.txt":                               TierAttributed,
		// Code that merely has a handoff or memory in its path is still code.
		"internal/memory/store.go":    TierImplementation,
		"internal/handoff/handoff.go": TierImplementation,
		"pkg/inmemory/cache.go":       TierImplementation,
	}
	for p, want := range cases {
		if got := TierOf(p); got != want {
			t.Errorf("TierOf(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestIsHandoff(t *testing.T) {
	for p, want := range map[string]bool{
		".claude/handoffs/PRIOR_SESSION_STATE.md": true,
		".planning/phase1/PLAN.md":                true,
		"docs/design.md":                          false,
		"internal/memory/store.go":                false,
		"internal/handoff/handoff.go":             false,
		"pkg/inmemory/cache.go":                   false,
		"docs/myclaude.md":                        false,
		"docs/inmemory/notes.md":                  false,
		".claude/projects/x/memory/note.md":       true,
		"memory/MEMORY.md":                        true,
		"docs/HANDOFF-2026-11-01.md":              true,
		"sub/CLAUDE.md":                           true,
		"AGENTS.md":                               true,
		".claude/handoffs/state.json":             true,
	} {
		if IsHandoff(p) != want {
			t.Errorf("IsHandoff(%q) = %v, want %v", p, !want, want)
		}
	}
}
