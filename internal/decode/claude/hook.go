// Package claude decodes Claude Code hook payloads and transcripts (verified on 2.1.316).
package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/decode"
)

const Harness = "claude-code"

var argKinds = map[string]decode.Kind{
	"session-start": decode.SessionStart, "user-prompt-submit": decode.UserPrompt, "stop": decode.Stop,
	"subagent-stop": decode.SubagentStop, "pre-compact": decode.PreCompact, "session-end": decode.SessionEnd,
}

var eventNames = map[decode.Kind]string{
	decode.SessionStart: "SessionStart", decode.UserPrompt: "UserPromptSubmit", decode.Stop: "Stop",
	decode.SubagentStop: "SubagentStop", decode.PreCompact: "PreCompact", decode.SessionEnd: "SessionEnd",
}

// KindFromArg maps the hook subcommand argument to an event kind.
func KindFromArg(arg string) (decode.Kind, bool) {
	k, ok := argKinds[arg]
	return k, ok
}

// payload follows the committed 2.1.316 fixtures (V06). Unknown keys (effort,
// permission_mode, session_crons, tool_* and any later additions) are ignored.
type payload struct {
	SessionID            string            `json:"session_id"`
	PromptID             string            `json:"prompt_id"`
	TranscriptPath       string            `json:"transcript_path"`
	Cwd                  string            `json:"cwd"`
	HookEventName        string            `json:"hook_event_name"`
	Source               string            `json:"source"`
	Prompt               string            `json:"prompt"`
	StopHookActive       bool              `json:"stop_hook_active"`
	LastAssistantMessage string            `json:"last_assistant_message"`
	AgentID              string            `json:"agent_id"`
	AgentType            string            `json:"agent_type"`
	AgentTranscriptPath  string            `json:"agent_transcript_path"`
	BackgroundTasks      []json.RawMessage `json:"background_tasks"`
}

var winAbs = regexp.MustCompile(`^([A-Za-z]:[\\/]|\\\\)`)

// isAbs accepts unix and Windows absolute paths whatever the host platform.
func isAbs(p string) bool { return strings.HasPrefix(p, "/") || winAbs.MatchString(p) }

func malformed(format string, a ...any) error {
	return fmt.Errorf("%w: %s", decode.ErrMalformed, fmt.Sprintf(format, a...))
}

// Decode reads one payload for the expected event kind.
func Decode(kind decode.Kind, r io.Reader) (decode.Event, error) {
	data, err := io.ReadAll(io.LimitReader(r, decode.MaxPayload+1))
	if err != nil {
		return decode.Event{}, err
	}
	if len(data) > decode.MaxPayload {
		return decode.Event{}, malformed("payload exceeds %d bytes", decode.MaxPayload)
	}
	var p payload
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&p); err != nil {
		return decode.Event{}, malformed("%v", err)
	}
	if want := eventNames[kind]; p.HookEventName != want {
		return decode.Event{}, malformed("hook_event_name %q, expected %q", p.HookEventName, want)
	}
	if p.SessionID == "" || len(p.SessionID) > 200 {
		return decode.Event{}, malformed("missing or oversized session_id")
	}
	if p.Cwd == "" || !isAbs(p.Cwd) {
		return decode.Event{}, malformed("cwd %q is not absolute", p.Cwd)
	}
	for _, tp := range []string{p.TranscriptPath, p.AgentTranscriptPath} {
		if tp != "" && !isAbs(tp) {
			return decode.Event{}, malformed("transcript path %q is not absolute", tp)
		}
	}
	return decode.Event{Kind: kind, Harness: Harness, SessionID: p.SessionID, PromptID: p.PromptID,
		TranscriptPath: p.TranscriptPath, Cwd: p.Cwd, Source: p.Source, Prompt: p.Prompt,
		LastAssistantMessage: p.LastAssistantMessage, StopHookActive: p.StopHookActive,
		AgentID: p.AgentID, AgentType: p.AgentType, AgentTranscriptPath: p.AgentTranscriptPath,
		BackgroundTasks: len(p.BackgroundTasks)}, nil
}
