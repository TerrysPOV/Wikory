package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const synthetic = `{"type":"user","message":{"role":"user","content":"Fix the queue"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Looking."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"ok"}]}}
not json at all
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/repo/src/q.go"}}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done: tests pass."}]}}
{"type":"assistant","message":{"role":"assist`

func TestReadSliceSynthetic(t *testing.T) {
	s, err := ReadSlice(strings.NewReader(synthetic), 0, int64(len(synthetic)))
	if err != nil {
		t.Fatal(err)
	}
	partialAt := int64(strings.LastIndex(synthetic, "\n") + 1)
	if s.End != partialAt {
		t.Fatalf("End=%d, want %d (the partial last line must be left unread)", s.End, partialAt)
	}
	if s.Skipped != 1 || len(s.ToolUses) != 2 || s.ToolUses[0].Command != "go test ./..." || s.ToolUses[1].FilePath != "/repo/src/q.go" {
		t.Fatalf("%+v", s)
	}
	if r, ok := s.Results["t1"]; !ok || r.IsError {
		t.Fatalf("result %+v %v", r, ok)
	}
	last := s.AssistantMessages[len(s.AssistantMessages)-1]
	if last.Text != "Done: tests pass." || last.End != partialAt {
		t.Fatalf("last assistant message %+v, want end %d", last, partialAt)
	}
	if s.ToolUses[1].Offset >= last.End || s.ToolUses[1].Offset <= s.ToolUses[0].Offset {
		t.Fatalf("tool use offsets out of order: %+v", s.ToolUses)
	}
}

func TestReadSliceFromOffset(t *testing.T) {
	start := int64(strings.Index(synthetic, "\n")+1) + int64(strings.Index(synthetic[strings.Index(synthetic, "\n")+1:], "\n")+1)
	s, _ := ReadSlice(strings.NewReader(synthetic), start, int64(len(synthetic))-start)
	if len(s.ToolUses) != 1 || s.ToolUses[0].Name != "Edit" {
		t.Fatalf("reading from the third line should see only the Edit tool use: %+v", s.ToolUses)
	}
}

func TestReadSliceRealFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixtures, "transcript-tool-use.jsonl"))
	if err != nil {
		t.Fatalf("fixture missing (S6 commits it): %v", err)
	}
	s, err := ReadSlice(strings.NewReader(string(data)), 0, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range s.ToolUses {
		if u.Name == "Bash" && strings.Contains(u.Command, "ttyprobe") {
			_, found = s.Results[u.ID]
		}
	}
	if !found {
		t.Fatalf("the Bash ttyprobe call and its result were not decoded: %+v", s.ToolUses)
	}
}

// Assistant lines inherit the prompt identity of the user line before them; hook
// continuation lines (isMeta) and non-conversational lines are never turn content.
func TestReadSliceTracksPromptIdentityAndDropsMetaLines(t *testing.T) {
	in := `{"type":"user","promptId":"p1","message":{"role":"user","content":"one"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"a1","name":"Bash","input":{"command":"go test ./a"}}]}}
{"type":"user","promptId":"p1","isMeta":true,"message":{"role":"user","content":"Stop hook feedback:\nWIKORY CONTINUATION"}}
{"type":"attachment","attachment":{"type":"text","content":[{"type":"text","text":"ATTACHED"}]}}
{"type":"api-request-blob","message":{"content":[{"type":"text","text":"BLOB"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}
{"type":"user","promptId":"p2","message":{"role":"user","content":"two"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}
`
	s, err := ReadSlice(strings.NewReader(in), 0, int64(len(in)))
	if err != nil {
		t.Fatal(err)
	}
	if s.Ignored != 3 || len(s.AssistantMessages) != 2 || len(s.ToolUses) != 1 {
		t.Fatalf("meta or non-conversational lines were decoded as content: %+v", s)
	}
	if s.ToolUses[0].PromptID != "p1" || s.AssistantMessages[0].PromptID != "p1" || s.AssistantMessages[1].PromptID != "p2" {
		t.Fatalf("prompt identity not inherited: %+v", s)
	}
}
