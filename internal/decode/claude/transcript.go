package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// ToolUse is one tool call seen in the transcript. Offset is the absolute offset of the line
// that carries it, so callers can bound facts to one turn's slice.
type ToolUse struct {
	ID       string
	Name     string
	FilePath string
	Command  string
	Offset   int64
	PromptID string // the turn identity in force at this line; "" when the harness wrote none
}

// Message is the joined text of one assistant line and the absolute offset just after it.
type Message struct {
	Text     string
	End      int64
	PromptID string
}

// ToolResult is the harness-recorded result of a tool call.
type ToolResult struct {
	ToolUseID string
	IsError   bool
	Offset    int64
	PromptID  string
}

// Slice is what a transcript range contains.
type Slice struct {
	AssistantMessages []Message
	ToolUses          []ToolUse
	Results           map[string]ToolResult
	End               int64
	Lines             int
	Skipped           int // lines that are not valid JSON
	Ignored           int // non-conversational and meta lines, never turn content (V17, V25)
	prompt            string
}

// tline is one transcript line. On Claude Code 2.1.316 user lines carry promptId, equal to
// the hooks' prompt_id (V07, evidence S5 and S9); assistant lines carry none and belong to
// the most recent user line's prompt.
type tline struct {
	Type     string `json:"type"`
	IsMeta   bool   `json:"isMeta"`
	PromptID string `json:"promptId"`
	Message  struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	ToolUseID string `json:"tool_use_id"`
	IsError   bool   `json:"is_error"`
	Input     struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	} `json:"input"`
}

var editTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

// ReadSlice decodes the complete lines in [start, start+limit).
func ReadSlice(r io.ReaderAt, start, limit int64) (Slice, error) {
	s := Slice{Results: map[string]ToolResult{}, End: start}
	br := bufio.NewReaderSize(io.NewSectionReader(r, start, limit), 1<<16)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			at := s.End
			s.End += int64(len(line))
			s.Lines++
			s.decodeLine(line, at, s.End)
		}
		if errors.Is(err, io.EOF) {
			return s, nil // a trailing line without a newline is still being written
		}
		if err != nil {
			return s, err
		}
	}
}

func (s *Slice) decodeLine(line []byte, at, end int64) {
	var l tline
	if json.Unmarshal(line, &l) != nil {
		s.Skipped++
		return
	}
	if l.Type == "user" && l.PromptID != "" {
		s.prompt = l.PromptID
	}
	// Only conversational lines are turn content. Hook continuation reasons are user lines
	// with isMeta (V17); attachment, api-request* and the bookkeeping types are dropped (V25).
	if (l.Type != "user" && l.Type != "assistant") || l.IsMeta {
		s.Ignored++
		return
	}
	var blocks []block
	if len(l.Message.Content) > 0 && l.Message.Content[0] == '[' {
		if json.Unmarshal(l.Message.Content, &blocks) != nil {
			s.Skipped++
			return
		}
	}
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if l.Type == "assistant" {
				texts = append(texts, b.Text)
			}
		case "tool_use":
			u := ToolUse{ID: b.ID, Name: b.Name, Command: b.Input.Command, Offset: at, PromptID: s.prompt}
			if editTools[b.Name] {
				u.FilePath = b.Input.FilePath
				if u.FilePath == "" {
					u.FilePath = b.Input.NotebookPath
				}
			}
			s.ToolUses = append(s.ToolUses, u)
		case "tool_result":
			s.Results[b.ToolUseID] = ToolResult{ToolUseID: b.ToolUseID, IsError: b.IsError, Offset: at, PromptID: s.prompt}
		}
	}
	if len(texts) > 0 {
		s.AssistantMessages = append(s.AssistantMessages, Message{Text: strings.Join(texts, "\n"), End: end, PromptID: s.prompt})
	}
}

// ForPrompt keeps only the facts carrying prompt id.
func (s Slice) ForPrompt(id string) Slice {
	out := Slice{Results: map[string]ToolResult{}, End: s.End, Lines: s.Lines, Skipped: s.Skipped, Ignored: s.Ignored}
	for _, m := range s.AssistantMessages {
		if m.PromptID == id {
			out.AssistantMessages = append(out.AssistantMessages, m)
		}
	}
	for _, u := range s.ToolUses {
		if u.PromptID == id {
			out.ToolUses = append(out.ToolUses, u)
		}
	}
	for k, v := range s.Results {
		if v.PromptID == id {
			out.Results[k] = v
		}
	}
	return out
}

// Before keeps only the facts on lines that start before the absolute offset limit.
func (s Slice) Before(limit int64) Slice {
	out := Slice{Results: map[string]ToolResult{}, End: min(s.End, limit), Lines: s.Lines, Skipped: s.Skipped, Ignored: s.Ignored}
	for _, m := range s.AssistantMessages {
		if m.End <= limit {
			out.AssistantMessages = append(out.AssistantMessages, m)
		}
	}
	for _, u := range s.ToolUses {
		if u.Offset < limit {
			out.ToolUses = append(out.ToolUses, u)
		}
	}
	for k, v := range s.Results {
		if v.Offset < limit {
			out.Results[k] = v
		}
	}
	return out
}
