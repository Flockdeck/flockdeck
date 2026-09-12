package chat

import (
	"strings"
	"testing"
)

// "(cleared)" alone leaves somebody wondering whether the conversation is gone;
// what /clear does and keeps is said.
func TestClearSaysWhatItKeeps(t *testing.T) {
	out := run(t, Options{Agent: "anthropic"}, "/clear\n/exit\n", &scriptedWire{})
	if !strings.Contains(out, "the model starts afresh") || !strings.Contains(out, "the transcript keeps") {
		t.Errorf("/clear did not say what it does:\n%s", out)
	}
}
