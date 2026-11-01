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
	"github.com/TerrysPOV/Wikory/internal/store"
)

func init() {
	register(Command{Name: "hook", Summary: "harness hook entry point (called by the plugin)", Raw: true, Run: runHook})
}

// testHookSlowBriefing lets a test push the briefing past its budget. Nil in production.
var testHookSlowBriefing func()

func hookBudget(env *Env) time.Duration {
	if ms, err := strconv.Atoi(env.Getenv("WIKORY_HOOK_BUDGET_MS")); err == nil && ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	s := 1
	if n, err := strconv.Atoi(env.Getenv("WIKORY_LATENCY_SLACK")); err == nil && n > 0 {
		s = n
	}
	return 2 * time.Second * time.Duration(s)
}

// logHookError records the kind of a hook failure, never its content.
func logHookError(env *Env, kind string) {
	dir, err := store.DataDir(env.Getenv)
	if err != nil {
		return
	}
	store.RestrictUmask()
	if store.EnsurePrivateDir(dir) != nil {
		return
	}
	f, err := openHookLog(filepath.Join(dir, "hook-errors.log")) // never blocks; regular files only
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), kind)
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
		TranscriptRoots: transcriptRoots(env), Now: time.Now, SealTimeout: 10 * time.Minute, StaleMarkerAfter: 6 * time.Hour}
}

// timeoutBriefing is the status-only fallback. It is a pure string computation, so it can be
// built before any I/O and returned unchanged when the deadline passes (it does no discovery).
func timeoutBriefing(reason string) string {
	return retrieve.StatusOnly(retrieve.BriefInput{Mode: "degraded", Degraded: reason, HardCharCap: retrieve.DefaultHardCharCap})
}

func runHook(ctx context.Context, env *Env, args []string) (res Result, err error) {
	start := time.Now()
	deadline := start.Add(hookBudget(env)) // the budget starts before stdin is read
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
	type decoded struct {
		ev  decode.Event
		err error
	}
	// Computed before stdin is read, so the stalled-input path does no work either.
	stdinFallback := timeoutBriefing("the hook input did not arrive within " + hookBudget(env).String())
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
		action = userPrompt(ctx, &e2, ev)
	case decode.Stop:
		action = stopCapture(ctx, &e2, ev)
	}
	return
}

func writeSessionStart(env *Env, text string) {
	out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "SessionStart", "additionalContext": text}})
	fmt.Fprintln(env.IO.Out, string(out))
}

// sessionStart answers within what is left of the hook's budget, which began before stdin.
func sessionStart(ctx context.Context, env *Env, ev decode.Event, deadline time.Time) string {
	budget := hookBudget(env)
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
	p, err := openProject(ctx, env, false)
	if err == errNotInitialised || err == errNotGit {
		return ""
	}
	if err != nil {
		logHookError(env, "open")
		_, root, derr := discover(ctx, env)
		if derr != nil {
			return degradedBriefing(nil, "the store could not be opened")
		}
		defer root.Close()
		return degradedBriefing(root, "the store could not be opened")
	}
	defer p.Close()
	if !servable(p) {
		return notRegisteredBriefing(p)
	}
	rec := newRecorder(p, env)
	_ = rec.SweepStale(ctx, ev.SessionID)
	_, _ = rec.Seal(ctx, deadline.Add(-time.Duration(float64(time.Until(deadline))*0.6)))
	text, err := buildBriefing(ctx, p, "full", deadline)
	if err != nil {
		logHookError(env, "briefing")
		return degradedBriefing(p.RepoRoot, "the index could not be read")
	}
	return text
}

func userPrompt(ctx context.Context, env *Env, ev decode.Event) string {
	p, err := openProject(ctx, env, false)
	if err != nil {
		return "skipped"
	}
	defer p.Close()
	if !servable(p) {
		return "unregistered"
	}
	rec := newRecorder(p, env)
	if err := rec.TurnStart(ctx, ev); err != nil {
		logHookError(env, "turn-start")
		return "error"
	}
	_, _ = rec.Seal(ctx, time.Now().Add(100*time.Millisecond))
	return "turn-start"
}

func stopCapture(ctx context.Context, env *Env, ev decode.Event) string {
	p, err := openProject(ctx, env, false)
	if err != nil {
		return "skipped"
	}
	defer p.Close()
	if !servable(p) {
		return "unregistered"
	}
	if _, _, err := newRecorder(p, env).Stop(ctx, ev); err != nil {
		logHookError(env, "capture")
		return "error"
	}
	return "captured"
}
