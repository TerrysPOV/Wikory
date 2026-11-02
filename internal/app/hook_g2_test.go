package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/decode"
	"github.com/TerrysPOV/Wikory/internal/store"
)

type hookCase struct {
	arg, event string
	extra      map[string]any
}

var allHooks = []hookCase{
	{"session-start", "SessionStart", map[string]any{"source": "startup"}},
	{"user-prompt-submit", "UserPromptSubmit", map[string]any{"prompt": "x", "prompt_id": "p1"}},
	{"stop", "Stop", map[string]any{"prompt_id": "p1", "last_assistant_message": "done"}},
}

// Each hook has its own time budget, a little under the plugin timeout that would kill it.
func TestHookBudgetsStayUnderThePluginTimeouts(t *testing.T) {
	e := &Env{Getenv: func(string) string { return "" }}
	want := map[decode.Kind]time.Duration{decode.SessionStart: 2 * time.Second, decode.UserPrompt: 2500 * time.Millisecond, decode.Stop: 4 * time.Second}
	for kind, d := range want {
		if got := hookBudget(e, kind); got != d {
			t.Fatalf("kind %v: budget %v, want %v", kind, got, d)
		}
	}
	data, err := os.ReadFile("../../plugins/claude/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Timeout int `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for event, kind := range map[string]decode.Kind{"SessionStart": decode.SessionStart, "UserPromptSubmit": decode.UserPrompt, "Stop": decode.Stop} {
		timeout := time.Duration(m.Hooks[event][0].Hooks[0].Timeout) * time.Second
		if b := hookBudget(e, kind); b+400*time.Millisecond > timeout {
			t.Fatalf("%s: budget %v leaves under 400ms before the plugin timeout %v", event, b, timeout)
		}
	}
}

// registry.lock is held by someone else: the store would wait up to five seconds for it, longer
// than the harness allows. Every hook must still return inside its budget and say it timed out.
func TestHooksReturnWithinBudgetWhileTheRegistryLockIsHeld(t *testing.T) {
	for _, hc := range allHooks {
		t.Run(hc.arg, func(t *testing.T) {
			h := newHookRig(t)
			h.c.extra["WIKORY_HOOK_BUDGET_MS"] = "400"
			unlock, err := store.Lock(filepath.Join(h.c.dataDir, "registry.lock"), 0)
			if err != nil {
				t.Fatal(err)
			}
			_, el, code := runHookTimed(t, h, hc.arg, h.payload(hc.event, h.dir, hc.extra), 9*time.Second)
			unlock()
			time.Sleep(300 * time.Millisecond) // let the abandoned work see its expired deadline
			if code != ExitOK || el > 400*time.Millisecond+400*time.Millisecond*slack() {
				t.Fatalf("code=%d elapsed=%v: the hook outlived its 400ms budget", code, el)
			}
			if h.logKindCount(t, "timeout") == 0 {
				t.Fatalf("no timeout entry in the hook error log: %q", h.logText(t))
			}
		})
	}
}

// Only a repository that is not git, or has no project-memory, is silent. Any other failure to
// open the project is logged, so a broken config does not look like a quiet session.
func TestHooksLogWhyTheProjectCouldNotBeOpened(t *testing.T) {
	h := newHookRig(t)
	if err := os.WriteFile(filepath.Join(h.dir, "project-memory", "config.toml"), []byte("this is [not toml"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, hc := range allHooks[1:] { // SessionStart already logged this (TestHookUnopenableStoreGetsStatusOnlyNeverRepoContent)
		out, _, code := h.hook(t, h.dir, hc.arg, h.payload(hc.event, h.dir, hc.extra))
		if code != ExitOK || out != "" {
			t.Fatalf("%s: code=%d out=%q", hc.arg, code, out)
		}
	}
	if h.logKindCount(t, "open") != 2 {
		t.Fatalf("a corrupt config.toml was not logged by both hooks: %q", h.logText(t))
	}
}

func TestHooksStaySilentOutsideAnInitialisedGitRepo(t *testing.T) {
	plain := newGitRepo(t) // git, but no project-memory
	notGit := t.TempDir()
	for _, dir := range []string{plain, notGit} {
		c := newCLI(t)
		for _, hc := range allHooks {
			c.stdin = `{"session_id":"s","cwd":"` + filepath.ToSlash(dir) + `","hook_event_name":"` + hc.event + `","prompt":"x","source":"startup","last_assistant_message":"d"}`
			if out, _, code := c.run(dir, "hook", hc.arg); code != ExitOK || out != "" {
				t.Fatalf("%s in %s: code=%d out=%q", hc.arg, dir, code, out)
			}
		}
		if b, err := os.ReadFile(filepath.Join(c.dataDir, "hook-errors.log")); err == nil {
			t.Fatalf("a repository without Wikory logged hook errors: %q", b)
		}
	}
}

// A stored turn whose record is corrupt cannot be sealed. The hook says so, and the session
// start still goes on to brief.
func TestSealFailureIsLogged(t *testing.T) {
	h := newHookRig(t)
	h.hook(t, h.dir, "user-prompt-submit", h.promptPayload("x", "p1"))
	h.hook(t, h.dir, "stop", h.stopPayload("p1", "done"))
	files, _ := filepath.Glob(filepath.Join(h.storeDir(t), "turns", "*", "*", "v0001.json"))
	if len(files) != 1 {
		t.Fatalf("want one stored turn, found %v", files)
	}
	if err := os.WriteFile(files[0], []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := h.context(t, h.dir); !strings.HasPrefix(got, "wikory: mode=full") {
		t.Fatalf("a corrupt turn stopped the briefing: %s", got)
	}
	if h.logKindCount(t, "seal") == 0 {
		t.Fatalf("the seal failure was not logged: %q", h.logText(t))
	}
	h.hook(t, h.dir, "user-prompt-submit", h.promptPayload("y", "p2"))
	if h.logKindCount(t, "seal") != 2 {
		t.Fatalf("UserPromptSubmit did not log its seal failure: %q", h.logText(t))
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// The harness closed our stdout: that is logged, and the hook still exits 0.
func TestSessionStartOutputFailureIsLoggedAndExitsZero(t *testing.T) {
	h := newHookRig(t)
	e := h.hookEnv(h.payload("SessionStart", h.dir, map[string]any{"source": "startup"}), brokenWriter{})
	if code := RunEnv([]string{"hook", "session-start"}, e); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if h.logKindCount(t, "output") == 0 {
		t.Fatalf("the failed write was not logged: %q", h.logText(t))
	}
}
