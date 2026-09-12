package chat

import (
	"strings"
	"testing"
)

// An answer with nothing in it says so, and /retry asks again, rather than the
// pane going back to the prompt as though it had been answered.
func TestAnEmptyAnswerSaysSoAndCanBeRetried(t *testing.T) {
	wire := &scriptedWire{turns: []turnFunc{says(""), says("here it is")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "hello\n/retry\n/exit\n", wire)
	if !strings.Contains(out, "(the model answered with nothing; /retry asks again)") {
		t.Errorf("the empty answer was not reported:\n%s", out)
	}
	if !strings.Contains(out, "here it is") {
		t.Errorf("/retry did not ask again:\n%s", out)
	}
	if reqs := wire.requests(); len(reqs) != 2 || len(reqs[1].Messages) != 1 {
		t.Errorf("/retry sent %+v", reqs)
	}
}
