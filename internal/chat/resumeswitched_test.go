package chat

import (
	"strings"
	"testing"
)

// A pane restarted with the agent's default model takes a conversation up with
// the model it had been switched to; a model picked for the pane itself wins.
func TestAResumedConversationKeepsTheModelItWasSwitchedTo(t *testing.T) {
	history := []Entry{
		{Type: "user", Text: "hi"},
		{Type: "assistant", Text: "hello", Model: "claude-opus-5"},
	}

	dir := t.TempDir()
	writeTranscript(t, dir, history...)
	wire := &scriptedWire{turns: []turnFunc{says("again")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-sonnet-5", DefaultModel: "claude-sonnet-5", Resume: true, Dir: dir},
		"go on\n/exit\n", wire)
	if reqs := wire.requests(); len(reqs) != 1 || reqs[0].Model != "claude-opus-5" {
		t.Errorf("a pane on the default asked %+v, want the model the conversation was switched to", reqs)
	}
	if !strings.Contains(out, "(answering with claude-opus-5, as this conversation last did") {
		t.Errorf("the model taken up again was not said:\n%s", out)
	}

	picked := t.TempDir()
	writeTranscript(t, picked, history...)
	wire = &scriptedWire{turns: []turnFunc{says("again")}}
	run(t, Options{Agent: "anthropic", Model: "claude-haiku-4-5", DefaultModel: "claude-sonnet-5", Resume: true, Dir: picked},
		"go on\n/exit\n", wire)
	if reqs := wire.requests(); len(reqs) != 1 || reqs[0].Model != "claude-haiku-4-5" {
		t.Errorf("a model picked for the pane was overridden: %+v", reqs)
	}
}
