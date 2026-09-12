package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// A standing permission is shown by /status and withdrawn by /forget, after
// which the next call of its family is asked about again.
func TestStandingPermissionsCanBeSeenAndWithdrawn(t *testing.T) {
	tool := &alwaysTool{fakeTool{name: "run_command", question: "run go test?", always: "go test", answer: "ok"}}
	call := func(id string) turnFunc {
		return asksFor(ToolCall{ID: id, Name: "run_command", Args: json.RawMessage(`{"command":"go test ./..."}`)})
	}
	wire := &scriptedWire{turns: []turnFunc{call("c1"), says("first done"), call("c2"), says("second done")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5", Tools: []Tool{tool}, Task: "test it"},
		"a\n/status\n/forget\n/forget\nagain\nn\n/exit\n", wire)

	said := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"allowed without asking: `go test`; /forget withdraws them",
		"withdrew 1 standing permission; every call is asked about again",
		"nothing has been allowed without asking",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// After /forget the second call was asked about, and declined.
	if strings.Count(out, "run go test?") != 2 || tool.ran() != 1 {
		t.Errorf("asked %d times and ran %d times, want asked twice and run once:\n%s",
			strings.Count(out, "run go test?"), tool.ran(), out)
	}
}
