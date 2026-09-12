package chat

import (
	"strings"
	"testing"
)

// A paste longer than a line may be is left out with a word about why, and the
// chat goes on reading -- rather than the input closing on it and the chat
// ending with nothing said.
func TestAnOverlongLineIsLeftOutAndReadingGoesOn(t *testing.T) {
	wire := &scriptedWire{turns: []turnFunc{says("got the short one")}}
	input := strings.Repeat("x", maxLine+10) + "\nthe short one\n/exit\n"
	out := run(t, Options{Agent: "anthropic"}, input, wire)

	if !strings.Contains(out, "was not sent") {
		t.Errorf("the overlong line was not explained:\n%.500s", out)
	}
	reqs := wire.requests()
	if len(reqs) != 1 || reqs[0].Messages[0].Text != "the short one" {
		t.Errorf("requests = %d, want one for the line after the overlong one", len(reqs))
	}
}
