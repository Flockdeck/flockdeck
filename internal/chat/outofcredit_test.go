package chat

import (
	"context"
	"strings"
	"testing"
	"time"
)

// An account out of credit is said to be out of credit, and is not asked
// again as though it were busy: OpenAI sends it as a 429, a rate limit's status.
func TestAnAccountOutOfCreditIsNotAskedAgain(t *testing.T) {
	saved := busyBackoff
	t.Cleanup(func() { busyBackoff = saved })
	busyBackoff = []time.Duration{time.Millisecond, time.Millisecond}

	for _, refusal := range []*apiError{
		{Code: 429, Status: "429 Too Many Requests", Msg: "You exceeded your current quota, please check your plan and billing details."},
		{Code: 400, Status: "400 Bad Request", Msg: "Your credit balance is too low to access the Anthropic API."},
	} {
		wire := &scriptedWire{turns: []turnFunc{
			func(context.Context, Request, func(Event)) error { return refusal },
			says("should not be reached"),
		}}
		out := run(t, Options{Agent: "openai", Model: "gpt-5"}, "hello\n/exit\n", wire)
		if n := len(wire.requests()); n != 1 {
			t.Errorf("%s: asked %d times, want once", refusal.Status, n)
		}
		said := strings.Join(strings.Fields(out), " ")
		if !strings.Contains(said, "the account behind this key is out of credit") ||
			!strings.Contains(said, "flockdeck keys set openai") || strings.Contains(said, "trying again") {
			t.Errorf("%s: not said as out of credit:\n%s", refusal.Status, out)
		}
	}

	// A rate limit is still a rate limit, and still asked again.
	if _, ok := busy(&apiError{Code: 429, Status: "429 Too Many Requests", Msg: "Rate limit reached for requests"}); !ok {
		t.Error("a plain rate limit is no longer taken as busy")
	}
}
