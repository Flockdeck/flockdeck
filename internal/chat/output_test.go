package chat

import (
	"strings"
	"testing"
)

// The pane shows one line of a tool's output as the call runs. When something
// went wrong, the rest is what the user wants to read, and it has to be
// somewhere they can get at.
func TestOutputShowsAllOfWhatAToolReturned(t *testing.T) {
	tool := &fakeTool{name: "run_command", answer: "--- FAIL: TestThing\n    thing_test.go:12: got 1, want 2\nFAIL"}
	wire := &scriptedWire{turns: []turnFunc{
		asksFor(ToolCall{ID: "c1", Name: "run_command"}),
		says("the test failed"),
	}}
	out := run(t, Options{Agent: "anthropic", Tools: []Tool{tool}, Task: "run the tests"},
		"/output\n/output 2\n/output x\n/exit\n", wire)

	if !strings.Contains(out, "(3 lines; /output shows them)") {
		t.Errorf("the summary does not say how to see the rest:\n%s", out)
	}
	if !strings.Contains(out, "thing_test.go:12: got 1, want 2") {
		t.Errorf("/output did not show the whole output:\n%s", out)
	}
	for _, want := range []string{"1 tool outputs so far", "/output takes a number"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}
