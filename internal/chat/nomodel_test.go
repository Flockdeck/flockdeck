package chat

import (
	"strings"
	"testing"
)

// An agent with no model named -- a local model server, as often as not -- is
// told so as it starts, and where to choose one, rather than learning it from
// the first answer failing.
func TestAChatWithNoModelSaysSoAtTheStart(t *testing.T) {
	out := run(t, Options{Agent: "openai-compatible"}, "/exit\n", &scriptedWire{})
	if !strings.Contains(out, "no model is named") || !strings.Contains(out, "/model") {
		t.Errorf("the start did not say no model is named:\n%s", out)
	}
	out = run(t, Options{Agent: "anthropic", Model: "claude-sonnet-5"}, "/exit\n", &scriptedWire{})
	if strings.Contains(out, "no model is named") {
		t.Errorf("a named model was said to be missing:\n%s", out)
	}
}
