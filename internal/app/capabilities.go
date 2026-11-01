package app

import (
	"context"
	"os/exec"
	"regexp"
	"time"
)

// Capabilities select the mode printed in every briefing (spec 8.1).
type Capabilities struct {
	CanContinueOnStop       bool `json:"can_continue_on_stop"`
	CanInjectOnSessionStart bool `json:"can_inject_on_session_start"`
	HasSubagents            bool `json:"has_subagents"`
	ModelPins               bool `json:"model_pins"`
	TranscriptStable        bool `json:"transcript_stable"`
}

// knownGood is filled from docs/verified-capabilities.md at the close of stage 0 (S14).
var knownGood = map[string]Capabilities{
	"2.1.316": {CanContinueOnStop: true, CanInjectOnSessionStart: true, HasSubagents: true, ModelPins: true, TranscriptStable: false},
}

// stage0Limits are verified or refuted stage 0 results that bound what Wikory may do, whatever the
// flags say (docs/spikes/results/consequences.section.md). Doctor prints them so a user can see them.
var stage0Limits = []string{
	"V09 refuted: Claude Code kills a hook's descendants, so hooks run no detached work; recovery is lazy, at the next operation",
	"V13: subagents share the parent session_id; self-exclusion keys on agent_id and agent_type",
	"V19: a plugin cannot supply statusLine; the status line must wrap the user's own settings",
	"V20: the autoMemoryDirectory redirect is read back as ordinary memory; the export fallback stands",
	"V18 and V29 are not verifiable here: /clear SessionStart and user-typed shell shortcuts are unobserved",
}

func modeFor(version string) (Capabilities, string) {
	if c, ok := knownGood[version]; ok && c.CanInjectOnSessionStart {
		return c, "full"
	}
	return Capabilities{}, "reduced"
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

func defaultProbeClaudeVersion(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		return ""
	}
	return versionRe.FindString(string(out))
}

var probeClaudeVersion = defaultProbeClaudeVersion
