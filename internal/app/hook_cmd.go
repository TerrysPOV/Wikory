package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/TerrysPOV/Wikory/internal/capture"
	"github.com/TerrysPOV/Wikory/internal/decode"
	"github.com/TerrysPOV/Wikory/internal/decode/claude"
	"github.com/TerrysPOV/Wikory/internal/retrieve"
)

func init() {
	register(Command{Name: "hook", Summary: "harness hook entry point (called by the plugin)", Raw: true, Run: runHook})
}

// testHookSlowBriefing lets a test push the briefing past its budget. Nil in production.
var testHookSlowBriefing func()

// hookBudget is the time one hook may spend, a little under the plugin timeout that would
// otherwise kill it silently (plugins/claude/hooks/hooks.json: SessionStart 5 s, UserPromptSubmit
// 3 s, Stop 5 s). WIKORY_HOOK_BUDGET_MS overrides it; WIKORY_LATENCY_SLACK multiplies it.
func hookBudget(env *Env, kind decode.Kind) time.Duration {
	if ms, err := strconv.Atoi(env.Getenv("WIKORY_HOOK_BUDGET_MS")); err == nil && ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	s := 1
	if n, err := strconv.Atoi(env.Getenv("WIKORY_LATENCY_SLACK")); err == nil && n > 0 {
		s = n
	}
	base := 2 * time.Second
	switch kind {
	case decode.UserPrompt:
		base = 2500 * time.Millisecond
	case decode.Stop:
		base = 4 * time.Second
	}
	return base * time.Duration(s)
}

func transcriptRoots(env *Env) []string {
	if v := env.Getenv("WIKORY_TRANSCRIPT_ROOTS"); v != "" {
		return filepath.SplitList(v)
	}
	if d := env.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return []string{filepath.Join(d, "projects")}
	}
	if h := env.Getenv("HOME"); h != "" {
		return []string{filepath.Join(h, ".claude", "projects")}
	}
	return nil
}

func newRecorder(p *project, env *Env) *capture.Recorder {
	return &capture.Recorder{Store: p.Store, CheckoutID: p.Checkout.ID, Repo: p.Repo, RepoRoot: p.RepoRoot,
		TranscriptRoots: transcriptRoots(env), Now: time.Now, SealTimeout: 10 * time.Minute, StaleMarkerAfter: 6 * time.Hour,
		Warn: func(kind string) { logHookError(env, kind) }}
}

// timeoutBriefing is the status-only fallback. It is a pure string computation, so it can be
// built before any I/O and returned unchanged when the deadline passes (it does no discovery).
func timeoutBriefing(reason string) string {
	return retrieve.StatusOnly(retrieve.BriefInput{Mode: "degraded", Degraded: reason, HardCharCap: retrieve.DefaultHardCharCap})
}

func runHook(ctx context.Context, env *Env, args []string) (res Result, err error) {
	start := time.Now()
	action := "none"
	defer func() {
		if r := recover(); r != nil {
			logHookError(env, "panic")
			action = "panic"
		}
		res = Result{Data: map[string]any{"action": action, "elapsed_ms": time.Since(start).Milliseconds()}}
		err = nil // a hook never fails the harness
	}()
	if len(args) != 1 {
		logHookError(env, "usage")
		return
	}
	kind, ok := claude.KindFromArg(args[0])
	if !ok {
		logHookError(env, "unknown-event")
		return
	}
	budget := hookBudget(env, kind)
	deadline := start.Add(budget) // the budget starts before stdin is read
	// One deadline for the whole hook. It reaches every database query and git command through
	// ctx, and bounded() enforces it on the waits ctx cannot reach (the registry lock, a read
	// of a file that never answers).
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	type decoded struct {
		ev  decode.Event
		err error
	}
	// Computed before stdin is read, so the stalled-input path does no work either.
	stdinFallback := timeoutBriefing("the hook input did not arrive within " + budget.String())
	got := make(chan decoded, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil { // a goroutine panic would end the process; a hook must exit 0
				got <- decoded{err: decode.ErrMalformed}
			}
		}()
		ev, err := claude.Decode(kind, env.IO.In)
		got <- decoded{ev, err}
	}()
	var ev decode.Event
	select {
	case d := <-got:
		if d.err != nil {
			logHookError(env, "malformed")
			return
		}
		ev = d.ev
	case <-time.After(time.Until(deadline)):
		logHookError(env, "stdin-timeout")
		if kind == decode.SessionStart && !env.JSON {
			action = "briefing"
			writeSessionStart(env, stdinFallback)
		}
		return
	}
	e2 := *env
	e2.Cwd = ev.Cwd
	switch kind {
	case decode.SessionStart:
		text := sessionStart(ctx, &e2, ev, deadline)
		action = "briefing"
		if text != "" && !env.JSON {
			writeSessionStart(env, text)
		}
	case decode.UserPrompt:
		action = bounded(env, deadline, func() string { return userPrompt(ctx, &e2, ev) })
	case decode.Stop:
		action = bounded(env, deadline, func() string { return stopCapture(ctx, &e2, ev) })
	}
	return
}

// bounded runs fn and gives up on it at the deadline, logging "timeout". The abandoned work
// is not waited for: the hook returns, the process exits, and its context is already cancelled,
// so it can start nothing new. A panic in fn is logged, never raised: a hook must exit 0.
func bounded(env *Env, deadline time.Time, fn func() string) string {
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logHookError(env, "panic")
				done <- "panic"
			}
		}()
		done <- fn()
	}()
	select {
	case s := <-done:
		return s
	case <-time.After(time.Until(deadline)):
		logHookError(env, "timeout")
		return "timeout"
	}
}

// writeSessionStart writes the briefing to the harness. A failed write (the harness closed our
// stdout) is logged as "output"; the hook still exits 0.
func writeSessionStart(env *Env, text string) {
	out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "SessionStart", "additionalContext": text}})
	if _, err := fmt.Fprintln(env.IO.Out, string(out)); err != nil {
		logHookError(env, "output")
	}
}

// openFailureIsNews reports whether a failure to open the project should be logged: not for a
// repository that is not git or has no project-memory (Wikory is not in use there), and not for
// a failure caused by the hook's own deadline (bounded logs that as "timeout").
func openFailureIsNews(ctx context.Context, err error) bool {
	return err != errNotGit && err != errNotInitialised && ctx.Err() == nil
}

// sessionStart answers within what is left of the hook's budget, which began before stdin.
func sessionStart(ctx context.Context, env *Env, ev decode.Event, deadline time.Time) string {
	budget := hookBudget(env, decode.SessionStart)
	slow := testHookSlowBriefing // read here, not in the goroutine, so tests can reset it without a data race
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logHookError(env, "panic")
				done <- degradedBriefing(nil, "an internal error occurred")
			}
		}()
		if slow != nil {
			slow()
		}
		done <- sessionStartBriefing(ctx, env, ev, deadline)
	}()
	// Computed before any I/O, so the timeout path does no discovery and no reads.
	fallback := timeoutBriefing("the briefing timed out after " + budget.String())
	select {
	case s := <-done:
		return s
	case <-time.After(time.Until(deadline)):
		logHookError(env, "timeout")
		return fallback
	}
}

func sessionStartBriefing(ctx context.Context, env *Env, ev decode.Event, deadline time.Time) string {
	mode := startModeProbe(ctx) // overlaps the project open and the index refresh
	p, err := openProject(ctx, env, false)
	if err == errNotInitialised || err == errNotGit {
		return ""
	}
	if err != nil {
		// Registration and trust cannot be confirmed without the store, so this is status only:
		// no repository file is read into the session (it may belong to a hostile checkout).
		logHookFailure(env, "open", err)
		return degradedBriefing(nil, "the store could not be opened")
	}
	defer p.Close()
	if !servable(p) {
		return notRegisteredBriefing(p)
	}
	rec := newRecorder(p, env)
	if err := rec.SweepStale(ctx, ev.SessionID); err != nil {
		logHookFailure(env, "sweep", err)
	}
	if _, err := rec.Seal(ctx, deadline.Add(-time.Duration(float64(time.Until(deadline))*0.6))); err != nil {
		logHookFailure(env, "seal", err)
	}
	text, err := buildBriefing(ctx, p, env, mode(), deadline)
	if err != nil {
		logHookFailure(env, "briefing", err)
		return degradedBriefing(p.RepoRoot, "the index could not be read")
	}
	return text
}

func userPrompt(ctx context.Context, env *Env, ev decode.Event) string {
	p, err := openProject(ctx, env, false)
	if err != nil {
		if openFailureIsNews(ctx, err) {
			logHookFailure(env, "open", err)
		}
		return "skipped"
	}
	defer p.Close()
	if !servable(p) {
		return "unregistered"
	}
	rec := newRecorder(p, env)
	if err := rec.TurnStart(ctx, ev); err != nil {
		logHookFailure(env, "turn-start", err)
		return "error"
	}
	if _, err := rec.Seal(ctx, time.Now().Add(100*time.Millisecond)); err != nil {
		logHookFailure(env, "seal", err)
	}
	return "turn-start"
}

func stopCapture(ctx context.Context, env *Env, ev decode.Event) string {
	p, err := openProject(ctx, env, false)
	if err != nil {
		if openFailureIsNews(ctx, err) {
			logHookFailure(env, "open", err)
		}
		return "skipped"
	}
	defer p.Close()
	if !servable(p) {
		return "unregistered"
	}
	if _, _, err := newRecorder(p, env).Stop(ctx, ev); err != nil {
		logHookFailure(env, "capture", err)
		return "error"
	}
	return "captured"
}
