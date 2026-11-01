// Package capture writes the deterministic turn record (spec 6, steps 1 and 2): the floor
// that exists whether or not any model ever enriches a turn.
package capture

import "github.com/TerrysPOV/Wikory/internal/ids"

// RecordSchema versions the turn file format.
const RecordSchema = "wikory.turn/v1"

// TurnID is deterministic, so replays and duplicate events are idempotent.
func TurnID(sessionID, marker string) string { return ids.Digest("turn", sessionID, marker) }

// Pointer locates the turn's slice of the harness transcript without an absolute path:
// Root is the digest of the approved transcript root, Rel the redacted slash path below it,
// and ID a content-independent identifier of the transcript.
type Pointer struct {
	Root        string `json:"root"`
	Rel         string `json:"rel"`
	ID          string `json:"id"`
	StartOffset int64  `json:"start_offset"`
	EndOffset   int64  `json:"end_offset,omitempty"`
}

// Fingerprint identifies an uncommitted file at capture time.
type Fingerprint struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// GitFacts are recorded privately for bookkeeping and never decide retrieval (spec 4.4).
type GitFacts struct {
	Branch string        `json:"branch"`
	Head   string        `json:"head"`
	Dirty  []Fingerprint `json:"dirty,omitempty"`
}

// Command is a shell command the agent ran.
type Command struct {
	Text      string `json:"text"`
	ToolUseID string `json:"tool_use_id"`
	Observed  bool   `json:"observed"`
	Failed    bool   `json:"failed"`
}

// Verification is observed only when the binary saw the tool result (spec 5.2).
type Verification struct {
	Command  string `json:"command"`
	Observed bool   `json:"observed"`
	Passed   bool   `json:"passed"`
}

// Record is one immutable version of a turn.
type Record struct {
	Schema           string         `json:"schema"`
	TurnID           string         `json:"turn_id"`
	Version          int            `json:"version"`
	State            string         `json:"state"`
	Outcome          string         `json:"outcome"`
	Stub             bool           `json:"stub"`
	Incomplete       bool           `json:"incomplete,omitempty"`
	Harness          string         `json:"harness"`
	SessionID        string         `json:"session_id"`
	Marker           string         `json:"marker"`
	CheckoutID       string         `json:"checkout_id"`
	Request          string         `json:"request"`
	FinalMessage     string         `json:"final_message"`
	FinalMessageHash string         `json:"final_message_hash"`
	Transcript       Pointer        `json:"transcript"`
	Git              GitFacts       `json:"git"`
	FilesTouched     []string       `json:"files_touched,omitempty"`
	Commands         []Command      `json:"commands,omitempty"`
	Verification     []Verification `json:"verification,omitempty"`
	Redactions       map[string]int `json:"redactions,omitempty"`
	SummaryOrigin    string         `json:"summary_origin"`
	CapturedAt       string         `json:"captured_at"`
	SealedAt         string         `json:"sealed_at,omitempty"`
}
