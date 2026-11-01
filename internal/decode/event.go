// Package decode turns raw harness payloads into Wikory's internal event set (spec 8.1).
// All payload and transcript parsing is confined to versioned, fixture-tested decoders.
package decode

import "errors"

// Kind is an internal event kind.
type Kind string

const (
	SessionStart Kind = "session-start"
	UserPrompt   Kind = "user-prompt"
	Stop         Kind = "stop"
	SubagentStop Kind = "subagent-stop"
	PreCompact   Kind = "pre-compact"
	SessionEnd   Kind = "session-end"
)

// MaxPayload bounds a hook payload.
const MaxPayload = 1 << 20

var ErrMalformed = errors.New("decode: malformed hook payload")

// Event is one decoded hook event.
type Event struct {
	Kind                 Kind
	Harness              string
	SessionID            string
	PromptID             string
	TranscriptPath       string
	Cwd                  string
	Source               string
	Prompt               string
	LastAssistantMessage string
	StopHookActive       bool
	AgentID              string // set on a subagent's events; subagents share the parent's SessionID (V13)
	AgentType            string
	AgentTranscriptPath  string
	BackgroundTasks      int // background tasks still listed on a Stop or SubagentStop (V15)
}
