package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/decode"
)

const fixtures = "../testdata/claude-2.1.316"

func decodeFixture(t *testing.T, name string, kind decode.Kind) decode.Event {
	t.Helper()
	f, err := os.Open(filepath.Join(fixtures, name))
	if err != nil {
		t.Fatalf("fixture %s missing (stage 0 S5 to S10 commit it): %v", name, err)
	}
	defer f.Close()
	ev, err := Decode(kind, f)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return ev
}

func TestDecodeRealFixtures(t *testing.T) {
	ss := decodeFixture(t, "session-start-startup.json", decode.SessionStart)
	ups := decodeFixture(t, "user-prompt-submit.json", decode.UserPrompt)
	stop := decodeFixture(t, "stop.json", decode.Stop)
	for _, ev := range []decode.Event{ss, ups, stop} {
		if ev.SessionID == "" || ev.Cwd == "" || ev.Harness != "claude-code" {
			t.Fatalf("%+v", ev)
		}
	}
	if ss.Source != "startup" || ups.Prompt == "" || stop.StopHookActive || stop.LastAssistantMessage == "" {
		t.Fatalf("ss=%+v ups=%+v stop=%+v", ss, ups, stop)
	}
	// V07 (verified): one turn's prompt_id is the same in UserPromptSubmit and Stop.
	if ups.PromptID == "" || ups.PromptID != stop.PromptID {
		t.Fatalf("prompt_id ups=%q stop=%q", ups.PromptID, stop.PromptID)
	}
	// These three fixtures come from S10, S9 and S7. A spike recorded not-verifiable-here
	// leaves its fixture absent; that is logged, not hidden, and the row in
	// docs/verified-capabilities.md says why.
	if optionalFixture(t, "session-start-clear.json") {
		if clear := decodeFixture(t, "session-start-clear.json", decode.SessionStart); clear.Source != "clear" {
			t.Fatalf("clear source %q", clear.Source)
		}
	}
	if optionalFixture(t, "stop-continuation-active.json") {
		if cont := decodeFixture(t, "stop-continuation-active.json", decode.Stop); !cont.StopHookActive {
			t.Fatal("continuation fixture should have stop_hook_active true")
		}
	}
	if optionalFixture(t, "subagent-stop.json") {
		sub := decodeFixture(t, "subagent-stop.json", decode.SubagentStop)
		if sub.AgentType == "" || sub.AgentID == "" || sub.AgentTranscriptPath == "" || sub.BackgroundTasks == 0 {
			t.Fatalf("subagent stop should carry agent_id, agent_type, agent_transcript_path and background_tasks (V13, V15): %+v", sub)
		}
	}
	// V07: SessionEnd carries the same prompt_id; SessionStart carries none.
	if end := decodeFixture(t, "session-end.json", decode.SessionEnd); end.PromptID != stop.PromptID || ss.PromptID != "" {
		t.Fatalf("prompt_id session-end=%q stop=%q session-start=%q", end.PromptID, stop.PromptID, ss.PromptID)
	}
}

func optionalFixture(t *testing.T, name string) bool {
	t.Helper()
	if _, err := os.Stat(filepath.Join(fixtures, name)); err != nil {
		t.Logf("fixture %s absent: its stage 0 spike was not run here (see docs/verified-capabilities.md)", name)
		return false
	}
	return true
}

func TestDecodeRejectsMalformed(t *testing.T) {
	good := `{"session_id":"s1","transcript_path":"/HOME/t.jsonl","cwd":"/HOME/p","hook_event_name":"Stop"}`
	cases := map[string]string{
		"not json":            `{`,
		"array":               `[]`,
		"wrong event name":    strings.Replace(good, `"Stop"`, `"SessionStart"`, 1),
		"no session":          strings.Replace(good, `"s1"`, `""`, 1),
		"relative cwd":        strings.Replace(good, `"/HOME/p"`, `"p"`, 1),
		"relative transcript": strings.Replace(good, `"/HOME/t.jsonl"`, `"t.jsonl"`, 1),
		// A valid Stop payload padded past the limit: only the size bound can reject it.
		"oversized": strings.Replace(good, `}`, `,"pad":"`+strings.Repeat("a", decode.MaxPayload)+`"}`, 1),
	}
	for name, in := range cases {
		_, err := Decode(decode.Stop, strings.NewReader(in))
		if !errors.Is(err, decode.ErrMalformed) {
			t.Errorf("%s: want ErrMalformed, got %v", name, err)
		}
		// The reader is also limited, so a truncated oversized payload would fail as bad JSON
		// anyway; asserting the reason is what makes the size bound itself observable.
		if name == "oversized" && (err == nil || !strings.Contains(err.Error(), "exceeds")) {
			t.Errorf("oversized: want a size error, got %v", err)
		}
	}
	if _, err := Decode(decode.Stop, strings.NewReader(strings.Replace(good, `}`, `,"future_field":1}`, 1))); err != nil {
		t.Errorf("unknown fields must be tolerated: %v", err)
	}
	if _, err := Decode(decode.Stop, strings.NewReader(strings.Replace(good, `"/HOME/p"`, `"C:\\Users\\x\\p"`, 1))); err != nil {
		t.Errorf("a Windows absolute cwd must be accepted on every platform: %v", err)
	}
}

func TestKindFromArg(t *testing.T) {
	if k, ok := KindFromArg("user-prompt-submit"); !ok || k != decode.UserPrompt {
		t.Fatal("user-prompt-submit")
	}
	if _, ok := KindFromArg("nonsense"); ok {
		t.Fatal("nonsense accepted")
	}
}
