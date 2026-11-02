package app

import (
	"context"
	"os/exec"
	"regexp"
	"sync"
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

// knownGood holds the capability flags for each Claude Code version that stage 0 verified, taken
// from docs/verified-capabilities.md. A version not listed here runs in reduced mode.
var knownGood = map[string]Capabilities{
	"2.1.316": {CanContinueOnStop: true, CanInjectOnSessionStart: true, HasSubagents: true, ModelPins: true, TranscriptStable: false},
}

// stage0Limits are verified or refuted stage 0 results that bound what Wikory may do, whatever the
// flags say (docs/spikes/results/consequences.section.md). Doctor prints them so a user can see them.
var stage0Limits = []string{
	"V09 refuted: Claude Code kills a hook's descendants, so hooks run no detached work; recovery is lazy, at the next operation",
	"V13: subagents share the parent session_id; self-exclusion keys on agent_id and agent_type",
	"V19 (static evidence only, not observed end to end): a plugin cannot supply statusLine; the status line must wrap the user's own settings",
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

// modeCache holds the mode probed in this process. A hook is a new process every time, so
// the cache saves the second probe of a long-lived caller (and of a test), not a hook's first.
var modeCache struct {
	mu   sync.Mutex
	done bool
	mode string
}

// probeBudget bounds the version probe inside a hook: `claude --version` takes about 0.3 s, and
// a hook has two seconds. A probe that does not answer in time leaves the mode reduced, which is
// the truthful answer when the harness cannot be identified (spec 8.1).
const probeBudget = 1200 * time.Millisecond

// briefingMode is the mode the briefing prints: the probed one (modeFor of the Claude Code
// version), computed once per process. A failed probe is cached too, as reduced.
func briefingMode(ctx context.Context) string {
	modeCache.mu.Lock()
	defer modeCache.mu.Unlock()
	if modeCache.done {
		return modeCache.mode
	}
	if ctx.Err() != nil {
		return "reduced" // the caller has already given up; do not start a probe nobody awaits
	}
	pctx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()
	_, mode := modeFor(probeClaudeVersion(pctx))
	if ctx.Err() == nil { // an answer cut short by the caller's own deadline is not worth keeping
		modeCache.done, modeCache.mode = true, mode
	}
	return mode
}

// startModeProbe begins the probe in the background and returns a function that waits for it,
// so the probe overlaps the work that does not need the mode.
func startModeProbe(ctx context.Context) func() string {
	ch := make(chan string, 1)
	go func() { ch <- briefingMode(ctx) }()
	return func() string { return <-ch }
}
