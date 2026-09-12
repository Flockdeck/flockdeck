package chat

import (
	"encoding/json"
	"slices"
	"testing"
)

// A question turns the pane amber; a no sends the model back to work, and the
// pane says so rather than staying amber until the turn ends.
func TestADeclinedCallPutsThePaneBackToWork(t *testing.T) {
	for _, answer := range []string{"n\n", "do it another way\n"} {
		pane := newPaneServer(t, "")
		tool := &fakeTool{name: "write_file", question: "write to a.txt?", answer: "wrote"}
		wire := &scriptedWire{turns: []turnFunc{
			asksFor(ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`{"path":"a.txt"}`)}),
			says("done"),
		}}
		run(t, Options{Agent: "anthropic", Tools: []Tool{tool}, Task: "write it",
			API: pane.srv.BaseURL(), Token: pane.srv.Token()}, answer, wire)

		names := pane.names()
		asked := slices.Index(names, "Notification:write_file")
		back := slices.Index(names, "PostToolUse:write_file")
		if asked < 0 || back < asked {
			t.Errorf("%q: the pane was not put back to work after the question: %q", answer, names)
		}
	}
}
