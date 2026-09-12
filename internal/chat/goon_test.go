package chat

import (
	"context"
	"strings"
	"testing"
)

// An answer cut off at its length limit is the start of the answer: it is
// kept, and the pane says to ask for the rest rather than to ask again.
func TestAnAnswerCutOffAtItsLimitIsGoneOnFrom(t *testing.T) {
	cut := func(_ context.Context, _ Request, emit func(Event)) error {
		emit(Event{Kind: EventText, Text: "the first half"})
		return &cutOffError{"the answer reached its limit of 100 tokens and was cut off there"}
	}
	wire := &scriptedWire{turns: []turnFunc{cut, says("the second half")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "write it\ngo on\n/exit\n", wire)

	if !strings.Contains(out, "say \"go on\" for the rest") {
		t.Errorf("the pane does not say how to get the rest:\n%s", out)
	}
	if strings.Contains(out, "could not answer") {
		t.Errorf("a cut-off answer is reported as no answer:\n%s", out)
	}
	reqs := wire.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	msgs := reqs[1].Messages
	if len(msgs) != 3 || msgs[1].Role != RoleAssistant || msgs[1].Text != "the first half" || msgs[2].Text != "go on" {
		t.Errorf("the second request did not carry the first half on: %+v", msgs)
	}
}
