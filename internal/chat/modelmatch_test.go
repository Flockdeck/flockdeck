package chat

import (
	"strings"
	"testing"
)

var matchChoices = []ModelChoice{
	{ID: "claude-opus-5", Name: "Opus 5"},
	{ID: "claude-sonnet-5", Name: "Sonnet 5"},
	{ID: "claude-haiku-4-5-20251001", Name: "Haiku 4.5"},
}

// A listed model is switched to by a part of its id or its name, since nobody
// remembers the date on the end of an id; a name that matches several switches
// to none of them, and one that matches nothing is taken as the id it is.
func TestModelIsChosenByPartOfItsName(t *testing.T) {
	for _, c := range []struct{ arg, want string }{
		{"opus", "claude-opus-5"},
		{"Sonnet 5", "claude-sonnet-5"},
		{"haiku", "claude-haiku-4-5-20251001"},
		{"CLAUDE-OPUS-5", "claude-opus-5"},
		{"my-own-model", "my-own-model"},
	} {
		wire := &scriptedWire{turns: []turnFunc{says("ok")}}
		out := run(t, Options{Agent: "anthropic", Models: matchChoices}, "/model "+c.arg+"\nhi\n/exit\n", wire)
		reqs := wire.requests()
		if len(reqs) != 1 || reqs[0].Model != c.want {
			t.Errorf("/model %s: asked %v, want %s\n%s", c.arg, reqs, c.want, out)
		}
	}
}

func TestAmbiguousModelNameSwitchesToNone(t *testing.T) {
	wire := &scriptedWire{turns: []turnFunc{says("ok")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-haiku-4-5-20251001", Models: matchChoices},
		"/model 5\n/model claude\nhi\n/exit\n", wire)
	if reqs := wire.requests(); len(reqs) != 1 || reqs[0].Model != "claude-haiku-4-5-20251001" {
		t.Errorf("an ambiguous name switched the model: %v", reqs)
	}
	if !strings.Contains(out, "more than one model goes by claude") || !strings.Contains(out, "claude-sonnet-5") {
		t.Errorf("the matches were not listed:\n%s", out)
	}
}
