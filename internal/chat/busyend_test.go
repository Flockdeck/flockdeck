package chat

import (
	"context"
	"strings"
	"testing"
	"time"
)

// An API still busy after the retries is said to be busy in the same words
// the retries were announced in, not by the stream's error type.
func TestAnAPIStillBusyAfterTheRetriesIsSaidInWords(t *testing.T) {
	saved := busyBackoff
	t.Cleanup(func() { busyBackoff = saved })
	busyBackoff = []time.Duration{time.Millisecond, time.Millisecond}

	overloaded := func(context.Context, Request, func(Event)) error {
		return &apiError{Code: 529, Status: "overloaded_error", Msg: "Overloaded"}
	}
	wire := &scriptedWire{turns: []turnFunc{overloaded, overloaded, overloaded}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "hello\n/exit\n", wire)

	said := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(said, "the API is overloaded: Overloaded") ||
		!strings.Contains(said, "(/retry asks again, once it is less busy)") {
		t.Errorf("the end of the retries was not said in words:\n%s", out)
	}
	if strings.Contains(out, "overloaded_error") || strings.Contains(out, "the model could not answer") {
		t.Errorf("the error was named the program's way:\n%s", out)
	}
	if n := len(wire.requests()); n != 3 {
		t.Errorf("made %d requests, want the first and two more", n)
	}
}
