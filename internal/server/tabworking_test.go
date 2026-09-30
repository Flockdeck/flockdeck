package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestATabWithBackgroundWorkIsSentAsWorking covers tabView.Working: the tab
// strip drew a mark only for a waiting agent, so an agent idle between turns
// with a background command running -- or one plainly working -- left its tab
// looking like any quiet one.
func TestATabWithBackgroundWorkIsSentAsWorking(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	p, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.CurrentTab().Tree.Panes()[0])
		p.Kind = session.KindAgent
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}
	working := func(s stateMsg) bool { return len(s.Tabs) > 0 && s.Tabs[0].Working }

	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	st := nextState(t, conn, working)
	if !working(st) {
		t.Fatal("a tab with an agent running background work is not sent as working")
	}
	if got := st.Panes[p.ID].Status; got == session.StatusWorking.String() {
		t.Errorf("the pane's status = %q: only the tab should say working", got)
	}

	p.Sess.SetBackground(nil)
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs) > 0 && !s.Tabs[0].Working })
	if working(st) {
		t.Error("the tab is still sent as working once the background work ended")
	}
}
