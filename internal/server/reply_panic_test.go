package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// TestAReplyThatPanicsLeavesTheInstanceRunning covers the work a reply to one
// window does on a goroutine of its own -- here, reading every agent's stored
// conversations, which other programs write. A panic there was recovered by
// nothing, and a panic nothing recovers ends the process: every agent in every
// project, over one listing.
func TestAReplyThatPanicsLeavesTheInstanceRunning(t *testing.T) {
	srv, ws := newTestServer(t)
	was := allConversations
	t.Cleanup(func() { allConversations = was })
	allConversations = func([]agent.Spec, string) ([]transcript.Conversation, error) {
		panic("a transcript nobody expected")
	}

	conn := dialControl(t, srv)
	c := &controlClient{out: make(chan []byte, 8)}
	srv.listConversations(c, ws.ActiveRoot())
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error {
		t.Fatalf("the panic was reported as %+v; want an error notice", note)
	}
	// The listing never answered, and the notice goes out only after it has
	// been forgotten: left behind, its window was tracked for as long as the
	// server ran.
	listings.Lock()
	_, tracked := listings.seq[c]
	listings.Unlock()
	if tracked {
		t.Error("a listing that panicked left its window tracked")
	}
	if v, ok := ask(srv, func() int { return 7 }); !ok || v != 7 {
		t.Fatalf("after the panic the workspace answered %d, %v; want 7, true", v, ok)
	}
}
