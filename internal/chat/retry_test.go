package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A turn that failed -- a dropped connection, an overloaded API -- is carried
// on with /retry, without the prompt having to be typed or pasted again, and
// without it being asked twice.
func TestRetryCarriesOnAFailedTurn(t *testing.T) {
	wire := &scriptedWire{turns: []turnFunc{
		func(context.Context, Request, func(Event)) error { return errors.New("connection reset by peer") },
		says("the second time"),
	}}
	out := run(t, Options{Agent: "anthropic", Task: "what is wrong?"}, "/retry\n/retry\n/exit\n", wire)

	if !strings.Contains(out, "/retry asks again") {
		t.Errorf("the failure does not say how to try again:\n%s", out)
	}
	if !strings.Contains(out, "the second time") {
		t.Errorf("/retry did not carry the turn on:\n%s", out)
	}
	reqs := wire.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	if n := len(reqs[1].Messages); n != 1 || reqs[1].Messages[0].Text != "what is wrong?" {
		t.Errorf("the retry sent %+v, want the one prompt once", reqs[1].Messages)
	}
	if !strings.Contains(out, "nothing to retry") {
		t.Errorf("a retry after an answered turn did not say there is nothing to do:\n%s", out)
	}
}

// An answer cut short -- by Ctrl+C, or a connection dropped part-way -- has
// words drawn before it stopped. /retry still carries the turn on, and asks
// again without the half-answer in place.
func TestRetryCarriesOnAnAnswerCutShort(t *testing.T) {
	wire := &scriptedWire{turns: []turnFunc{
		func(_ context.Context, _ Request, emit func(Event)) error {
			emit(Event{Kind: EventText, Text: "word0 word1 word2 "})
			return errors.New("connection reset by peer")
		},
		says("the whole answer"),
	}}
	out := run(t, Options{Agent: "anthropic", Task: "tell me slowly"}, "/retry\n/exit\n", wire)

	if strings.Contains(out, "nothing to retry") || !strings.Contains(out, "the whole answer") {
		t.Errorf("/retry did not carry on the answer cut short:\n%s", out)
	}
	reqs := wire.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	if last := reqs[1].Messages[len(reqs[1].Messages)-1]; last.Role != RoleUser {
		t.Errorf("the retry ended on %+v, want the prompt with no half-answer after it", last)
	}
}
