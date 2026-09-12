package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// /clear starts the conversation over but not the pane's standing permission,
// and says so, rather than leaving a new task to inherit it unannounced.
func TestClearSaysWhatIsStillAllowed(t *testing.T) {
	tool := &alwaysTool{fakeTool{name: "write_file", question: "write it?", always: "edits to files", answer: "wrote"}}
	wire := &scriptedWire{turns: []turnFunc{
		asksFor(ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`{"path":"a.txt"}`)}),
		says("done"),
	}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5", Tools: []Tool{tool}, Task: "write it"},
		"a\n/clear\n/forget\n/clear\n/exit\n", wire)

	said := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(said, "(still allowed without asking: `edits to files`; /forget withdraws them)") {
		t.Errorf("/clear did not say what is still allowed:\n%s", out)
	}
	if strings.Count(out, "still allowed without asking") != 1 {
		t.Errorf("/clear after /forget still spoke of standing permission:\n%s", out)
	}
}
