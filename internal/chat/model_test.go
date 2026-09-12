package chat

import (
	"strings"
	"testing"
)

// Switching models should not need the exact id to hand: /model lists what
// the agent offers, marks the one answering, and takes a number.
func TestModelListsTheChoicesAndTakesANumber(t *testing.T) {
	wire := &scriptedWire{turns: []turnFunc{says("from the second")}}
	out := run(t, Options{
		Agent: "anthropic", Model: "first",
		Models: []ModelChoice{{ID: "first", Name: "First", Note: "most capable"}, {ID: "second"}},
	}, "/model\n/model 2\n/model 9\nask\n/exit\n", wire)

	for _, want := range []string{"* 1  first  First — most capable", "  2  second", "/model <number>", "no model 9"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	reqs := wire.requests()
	if len(reqs) != 1 || reqs[0].Model != "second" {
		t.Fatalf("asked %+v, want one request to the model picked by number", reqs)
	}
}
