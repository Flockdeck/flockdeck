package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// A declined call says so, and a reply in words says where it went: somebody
// who typed "yes please" -- not one of the letters -- would otherwise go on
// believing the call ran.
func TestADeclinedCallSaysItWasNotRun(t *testing.T) {
	for _, c := range []struct{ answer, want string }{
		{"n\n", "  not run\n"},
		{"yes please\n", "not run; what you typed goes to the model instead"},
	} {
		tool := &fakeTool{name: "write_file", question: "write to a.txt?", answer: "wrote"}
		wire := &scriptedWire{turns: []turnFunc{
			asksFor(ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`{"path":"a.txt"}`)}),
			says("done"),
		}}
		out := run(t, Options{Agent: "anthropic", Tools: []Tool{tool}, Task: "write it"}, c.answer, wire)
		if tool.ran() != 0 {
			t.Errorf("%q ran the tool", c.answer)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%q: the pane does not say %q:\n%s", c.answer, c.want, out)
		}
	}
}
