package chat

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A busy API is said to be busy in words, whatever the status or the
// stream's error type called it.
func TestABusyAPIIsSaidInWords(t *testing.T) {
	saved := busyBackoff
	t.Cleanup(func() { busyBackoff = saved })
	busyBackoff = []time.Duration{time.Millisecond, time.Millisecond}

	overloaded := func(context.Context, Request, func(Event)) error {
		return &apiError{Code: 529, Status: "overloaded_error", Msg: "Overloaded"}
	}
	wire := &scriptedWire{turns: []turnFunc{overloaded, says("answered after all")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "hello\n/exit\n", wire)

	if !strings.Contains(out, "(the API is overloaded; trying again in") || strings.Contains(out, "overloaded_error;") {
		t.Errorf("the busy API was not said in words:\n%s", out)
	}
	if !strings.Contains(out, "answered after all") {
		t.Errorf("the second try was not made:\n%s", out)
	}

	for code, want := range map[int]string{
		429: "the API is limiting how often this key asks",
		503: "the API is failing on its side (503 Service Unavailable)",
	} {
		if got := busyWords(&apiError{Code: code, Status: map[int]string{429: "429 Too Many Requests", 503: "503 Service Unavailable"}[code]}); got != want {
			t.Errorf("busyWords(%d) = %q, want %q", code, got, want)
		}
	}
}
