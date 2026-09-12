package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// "stop" typed at a tool's question ends the turn there, as Ctrl+C would,
// rather than going to the model as what to do instead while it carries on.
func TestStopAtAQuestionEndsTheTurn(t *testing.T) {
	tool := &fakeTool{name: "write_file", question: "write it?", answer: "wrote"}
	wire := &scriptedWire{turns: []turnFunc{
		asksFor(
			ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`{"path":"a.txt"}`)},
			ToolCall{ID: "c2", Name: "write_file", Args: json.RawMessage(`{"path":"b.txt"}`)},
		),
		says("carried on"),
	}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5", Tools: []Tool{tool}, Task: "write both"},
		"Stop\n/retry\n/exit\n", wire)

	if tool.ran() != 0 {
		t.Errorf("the tool ran %d times after stop", tool.ran())
	}
	if !strings.Contains(out, "(stopped; /retry carries on)") {
		t.Errorf("the stop was not said:\n%s", out)
	}
	reqs := wire.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want the first and the one /retry made", len(reqs))
	}
	// The model is told the turn was stopped, not given "stop" to act on.
	for _, m := range reqs[1].Messages {
		if m.Role == RoleTool && strings.Contains(m.Text, "said: ") {
			t.Errorf("the reply went to the model as an instruction: %q", m.Text)
		}
	}
	if !strings.Contains(out, "carried on") {
		t.Errorf("/retry did not carry on:\n%s", out)
	}
}
