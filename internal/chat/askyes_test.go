package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// The single words that mean yes run the call; a longer reply that only
// starts with one is still what to do instead.
func TestAQuestionTakesTheWordsThatMeanYes(t *testing.T) {
	for answer, wantRun := range map[string]int{
		"ok\n":              1,
		"Sure\n":            1,
		"yeah!\n":           1,
		"ok, but with -v\n": 0,
		"nope\n":            0,
	} {
		tool := &fakeTool{name: "write_file", question: "write to a.txt?", answer: "wrote"}
		wire := &scriptedWire{turns: []turnFunc{
			asksFor(ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`{"path":"a.txt"}`)}),
			says("done"),
		}}
		out := run(t, Options{Agent: "anthropic", Tools: []Tool{tool}, Task: "write it"}, answer, wire)
		if tool.ran() != wantRun {
			t.Errorf("%q: the tool ran %d times, want %d\n%s", strings.TrimSpace(answer), tool.ran(), wantRun, out)
		}
	}
}
