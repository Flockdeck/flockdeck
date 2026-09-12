package chat

import (
	"context"
	"strings"
	"testing"
)

// A call that runs without a question, because of an "always" given earlier,
// says so: otherwise commands run unannounced and nobody watching can tell
// which yes they are running on.
func TestACallAllowedForTheSessionSaysSo(t *testing.T) {
	tool := &alwaysTool{fakeTool: fakeTool{name: "run_command", question: "run it?", answer: "ok"}}
	tool.always = "go test"
	s, out := newTestSession(t, "a\n", nil, tool)
	calls := []ToolCall{{ID: "c1", Name: "run_command"}, {ID: "c2", Name: "run_command"}}
	s.runCalls(context.Background(), calls)

	if tool.ran() != 2 {
		t.Fatalf("ran %d times, want both", tool.ran())
	}
	if strings.Count(out.String(), "run it?") != 1 {
		t.Errorf("asked more than once:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "allowed for the rest of the session: `go test`") {
		t.Errorf("the second call ran unannounced:\n%s", out.String())
	}
}
