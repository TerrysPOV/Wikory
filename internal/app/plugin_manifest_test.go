package app

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/decode/claude"
)

func TestPluginHooksCallTheBinaryInExecForm(t *testing.T) {
	data, err := os.ReadFile("../../plugins/claude/hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string   `json:"type"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
				Timeout int      `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"SessionStart": "session-start", "UserPromptSubmit": "user-prompt-submit", "Stop": "stop"}
	if len(m.Hooks) != len(want) {
		t.Fatalf("1a registers exactly %d events, found %d", len(want), len(m.Hooks))
	}
	for event, arg := range want {
		groups := m.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s: want one hook", event)
		}
		h := groups[0].Hooks[0]
		if h.Type != "command" || h.Command != "wikory" || len(h.Args) != 2 || h.Args[0] != "hook" || h.Args[1] != arg || h.Timeout < 1 || h.Timeout > 10 {
			t.Fatalf("%s: %+v", event, h)
		}
		if _, ok := claude.KindFromArg(h.Args[1]); !ok {
			t.Fatalf("%s: the binary does not accept %q", event, h.Args[1])
		}
	}
}
