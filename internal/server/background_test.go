package server

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestBackgroundWorkReachesTheWindow covers paneView.Background and
// agentView.Background: an agent's turn can end with a background command or
// subagent still running, and it then reads idle like one that is done. The
// count is what tells the two apart, in the pane's header and in the list of
// agents, so it has to reach both -- and go again once the work has ended.
func TestBackgroundWorkReachesTheWindow(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	// A shell pane standing in for an agent, the way close_test.go's do: a
	// shell's own count is never sent, since it has no hooks to keep one.
	p, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.CurrentTab().Tree.Panes()[0])
		p.Kind = session.KindAgent
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}
	id := p.ID

	listed := func(want int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			var ag agentsMsg
			sendCmd(t, conn, command{Cmd: "agents"})
			readUntil(t, conn, "agents", &ag)
			got := -1
			for _, a := range ag.Items {
				if a.PaneID == id {
					got = a.Background
				}
			}
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the agents list counts %d background tasks, want %d", got, want)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	p.Sess.NoteBackground("SubagentStart", hooks.BackgroundStart, "agent:a1")
	st := nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].Background == 2 })
	if got := st.Panes[id].Background; got != 2 {
		t.Fatalf("the pane's view counts %d background tasks, want 2", got)
	}
	listed(2)

	p.Sess.SetBackground(nil)
	st = nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].Background == 0 })
	if got := st.Panes[id].Background; got != 0 {
		t.Fatalf("the pane's view still counts %d background tasks once they ended", got)
	}
	listed(0)
}
