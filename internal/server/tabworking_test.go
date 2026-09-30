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

// TestAProjectWithBackgroundWorkIsSentWithItsCount covers projectView.Background:
// the rail's mark needs to tell a project whose agent is idle over background
// work from a quiet one, while Working, which the tallies read, stays zero.
func TestAProjectWithBackgroundWorkIsSentWithItsCount(t *testing.T) {
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
	bg := func(s stateMsg) int {
		for _, pr := range s.Projects {
			if pr.Active {
				return pr.Background
			}
		}
		return -1
	}

	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	st := nextState(t, conn, func(s stateMsg) bool { return bg(s) == 1 })
	if bg(st) != 1 {
		t.Fatalf("the project's background = %d, want 1", bg(st))
	}
	if st.Working != 0 {
		t.Errorf("the top bar's working = %d: background work must not change it", st.Working)
	}
	if got := st.Panes[p.ID]; got.Status == session.StatusWorking.String() || got.Background != 1 {
		t.Errorf("pane status %q background %d: status must stay idle and carry the count", got.Status, got.Background)
	}

	p.Sess.SetBackground(nil)
	st = nextState(t, conn, func(s stateMsg) bool { return bg(s) == 0 })
	if bg(st) != 0 {
		t.Errorf("the project's background = %d once it ended, want 0", bg(st))
	}
}
