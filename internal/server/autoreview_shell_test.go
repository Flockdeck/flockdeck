package server

import (
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// agentFirstPane is firstPane for a test that needs an agent pane: the test
// server's own first pane is a shell, which auto-review refuses.
func agentFirstPane(t *testing.T, srv *Server, ws *workspace.Workspace) string {
	t.Helper()
	id := firstPane(t, srv, ws)
	if _, ok := ask(srv, func() bool { ws.Pane(id).Kind = session.KindAgent; return true }); !ok {
		t.Fatal("the workspace did not answer")
	}
	return id
}

// Auto-review acts on an agent's PreToolUse calls and a shell makes none, so
// the command is refused for a shell pane, with a notice, and nothing changes.
func TestAutoReviewIsRefusedForAShellPane(t *testing.T) {
	srv, ws := newTestServer(t)
	id := addPane(t, srv, ws, "shell")
	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "autoReview", ID: id, AutoReview: true})
	untilExport(t, "the notice", func() bool { return len(c.out) > 0 })
	n := exportNotices(c)
	if len(n) != 1 || !n[0].Error || !strings.Contains(n[0].Text, "agent panes") {
		t.Errorf("notices = %+v, want one error saying auto-review is for agent panes", n)
	}
	if on, ok := ask(srv, func() bool { return ws.Pane(id).AutoReview }); !ok || on {
		t.Errorf("a shell pane was switched to auto-review (ok=%v, on=%v)", ok, on)
	}
}

// An agent pane still takes the command, and is not sent a notice.
func TestAutoReviewIsAcceptedForAnAgentPane(t *testing.T) {
	srv, ws := newTestServer(t)
	id := agentFirstPane(t, srv, ws)
	c := &controlClient{out: make(chan []byte, 8)}
	srv.handleCommand(c, command{Cmd: "autoReview", ID: id, AutoReview: true})
	untilExport(t, "auto-review to turn on", func() bool {
		on, _ := ask(srv, func() bool { return ws.Pane(id).AutoReview })
		return on
	})
	time.Sleep(20 * time.Millisecond)
	if n := exportNotices(c); len(n) != 0 {
		t.Errorf("an agent pane was sent %+v", n)
	}
}
