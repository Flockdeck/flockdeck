package structured

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
)

// While the agent works on a turn of its own, a prompt waits in the queue, where
// the person can see it. It goes out when the agent says the turn is over.
func TestPopHoldsPromptsWhileTheAgentIsBusyOnItsOwn(t *testing.T) {
	br := bridge.New(bridge.Config{})
	r := &Runtime{br: br, phase: PhaseReady, queue: []queued{{id: 1, blocks: []string{"a"}}}}
	br.Update(acp.Update{Kind: acp.UpdateBusy, Busy: true})
	if _, ok := r.pop(); ok {
		t.Fatal("a prompt was taken while the agent was busy")
	}
	if !r.Snapshot().Background {
		t.Error("the snapshot does not say the agent is busy")
	}
	if n := len(r.Snapshot().Queue); n != 1 {
		t.Errorf("queue has %d items, want 1", n)
	}
	br.Update(acp.Update{Kind: acp.UpdateBusy})
	if q, ok := r.pop(); !ok || q.id != 1 {
		t.Errorf("pop after the turn = %+v %v", q, ok)
	}
}
