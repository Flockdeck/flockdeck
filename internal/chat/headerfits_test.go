package chat

import (
	"strings"
	"testing"
)

// The line a chat opens with fits a pane of seventy columns for the usual
// agent and model, rather than breaking to leave a word on a line of its own.
func TestTheOpeningLineFitsAPaneSideBySide(t *testing.T) {
	out := run(t, Options{Agent: "anthropic", Model: "claude-sonnet-5"}, "/exit\n", &scriptedWire{})
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.HasPrefix(first, "flockdeck chat · anthropic · claude-sonnet-5 · /help") {
		t.Fatalf("the opening line is %q", first)
	}
	if w := displayWidth(first); w > 70 {
		t.Errorf("the opening line is %d columns, wider than a pane of 70: %q", w, first)
	}
}
