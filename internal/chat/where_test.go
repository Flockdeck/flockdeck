package chat

import (
	"strings"
	"testing"
)

// Everything the model does with the tools depends on where it is working and
// on which system, and a chat run by hand has no briefing to say so. The system
// prompt says both, and says them again after /clear.
func TestTheModelIsToldWhereItIsWorking(t *testing.T) {
	wire := &scriptedWire{turns: []turnFunc{says("one"), says("two")}}
	run(t, Options{Agent: "anthropic", Cwd: "/work/project"}, "hi\n/clear\nagain\n/exit\n", wire)
	reqs := wire.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	for i, r := range reqs {
		if !strings.Contains(r.System, "You are working in /work/project, on "+osName()+".") {
			t.Errorf("request %d's system prompt does not say where:\n%s", i+1, r.System)
		}
	}
}
