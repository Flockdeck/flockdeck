package server

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// The consumers the graph test does not reach are run through a fake pane too:
// the helper count and cap, which follow the tree of panes, and recording start,
// which asks the runtime where the conversation is kept. The pane below is told
// to be Claude Code, whose conversation can be recorded, and its runtime says it
// keeps none, so recording is refused: the runtime is read and the agent id is
// not.
func TestHelperCapAndRecordingReadTheRuntime(t *testing.T) {
	e := newGraphEnv(t)
	start := time.Now().Add(-time.Minute).Truncate(time.Second)
	for _, id := range []string{e.boss, e.dev, e.eyes, e.nested} {
		e.detach(id, fakeRuntime{status: session.StatusWorking, started: start, ran: true})
	}
	ask(e.srv, func() bool { e.ws.Pane(e.dev).Agent = "claude"; return true })

	if n, _ := ask(e.srv, func() int { return e.ws.HelpersBelow(e.boss) }); n != 3 {
		t.Errorf("%d helpers below the manager, want 3 whatever runs in them", n)
	}
	if err, _ := ask(e.srv, func() error { return e.ws.HelperRoom(e.boss) }); err != nil {
		t.Errorf("no room for a helper below a manager with three: %v", err)
	}

	found, err := e.ws.SetPaneRecording(e.dev, true)
	if !found || err == nil || !strings.Contains(err.Error(), "nothing to record") {
		t.Errorf("recording a pane whose runtime keeps no conversation: found %v, error %v", found, err)
	}
}
