package chat

import (
	"context"
	"strings"
	"testing"
)

// Arguments that are not JSON are kept to tell the model about, and the call
// that goes back to the API carries an object that parses.
func TestArgumentsThatAreNotJSONAreKept(t *testing.T) {
	var c callBuffer
	c.id, c.name = "c1", "run_command"
	c.args.WriteString(`{"command": "go test ./...",}`)
	got := c.done()
	if string(got.Args) != "{}" || got.BadArgs != `{"command": "go test ./...",}` {
		t.Errorf("done() = %s, %q", got.Args, got.BadArgs)
	}

	var empty callBuffer
	if got := empty.done(); string(got.Args) != "{}" || got.BadArgs != "" {
		t.Errorf("no arguments at all: done() = %s, %q; want an empty object and nothing wrong", got.Args, got.BadArgs)
	}
}

// A call whose arguments were not JSON is neither asked about nor run, and the
// model is told what was wrong with what it wrote rather than being told by
// the tool that it wrote nothing.
func TestACallWithArgumentsThatAreNotJSONIsNotRun(t *testing.T) {
	tool := &fakeTool{name: "run_command", question: "run it?", answer: "ran"}
	s, out := newTestSession(t, "", nil, tool)
	s.runCalls(context.Background(), []ToolCall{{ID: "c1", Name: "run_command", Args: []byte("{}"), BadArgs: `{"command": "ls",}`}})
	if tool.ran() != 0 {
		t.Error("the call was run")
	}
	if strings.Contains(out.String(), "run it?") {
		t.Errorf("the user was asked about it:\n%s", out)
	}
	if len(s.messages) != 1 || !strings.Contains(s.messages[0].Text, `{"command": "ls",}`) ||
		!strings.Contains(s.messages[0].Text, "not valid JSON") {
		t.Errorf("the model was told %+v", s.messages)
	}
}
