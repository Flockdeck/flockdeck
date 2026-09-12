package chat

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// A connection that drops before any of the answer arrives is asked again, as
// a busy API is; one that drops part-way is left to /retry, since asking again
// would draw what was shown twice.
func TestAConnectionDroppedBeforeTheAnswerIsAskedAgain(t *testing.T) {
	saved := busyBackoff
	t.Cleanup(func() { busyBackoff = saved })
	busyBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	reset := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}

	early := func(context.Context, Request, func(Event)) error { return reset }
	wire := &scriptedWire{turns: []turnFunc{early, says("answered after all")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "hello\n/exit\n", wire)
	if !strings.Contains(out, "(the connection dropped before any of the answer came; trying again in") ||
		!strings.Contains(out, "answered after all") || len(wire.requests()) != 2 {
		t.Errorf("an early drop was not asked again (%d requests):\n%s", len(wire.requests()), out)
	}

	late := func(_ context.Context, _ Request, emit func(Event)) error {
		emit(Event{Kind: EventText, Text: "half an"})
		return reset
	}
	wire = &scriptedWire{turns: []turnFunc{late, says("should not be asked")}}
	out = run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "hello\n/exit\n", wire)
	if len(wire.requests()) != 1 || strings.Contains(out, "trying again") {
		t.Errorf("a drop part-way was asked again (%d requests):\n%s", len(wire.requests()), out)
	}
}
