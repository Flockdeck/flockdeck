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
	working := func(s stateMsg) bool { return len(s.Tabs) > 0 && s.Tabs[0].Activity == workspace.ActivityWorking }

	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	st := nextState(t, conn, working)
	if !working(st) {
		t.Fatal("a tab with an agent running background work is not sent as working")
	}
	if got := st.Panes[p.ID].Status; got == session.StatusWorking.String() {
		t.Errorf("the pane's status = %q: only the tab should say working", got)
	}

	p.Sess.SetBackground(nil)
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs) > 0 && s.Tabs[0].Activity == workspace.ActivityNone })
	if working(st) {
		t.Error("the tab is still sent as working once the background work ended")
	}
}

// TestBackgroundWorkIsCountedAsWorkingInTheSnapshot covers the tallies: the
// tab, the pane header and the rail marked an agent idle over background work
// as working while the top bar, the window title, the favicon and the rail's
// count read 0. The snapshot now counts it, and the pane's status stays idle.
func TestBackgroundWorkIsCountedAsWorkingInTheSnapshot(t *testing.T) {
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
	projectWorking := func(s stateMsg) int {
		for _, pr := range s.Projects {
			if pr.Active {
				return pr.Working
			}
		}
		return -1
	}

	p.Sess.NoteBackground("PostToolUse", hooks.BackgroundStart, "shell:b1")
	st := nextState(t, conn, func(s stateMsg) bool { return s.Working == 1 })
	if st.Working != 1 || projectWorking(st) != 1 {
		t.Fatalf("top bar working = %d, project working = %d, want 1 and 1", st.Working, projectWorking(st))
	}
	if got := st.Panes[p.ID]; got.Status != session.StatusIdle.String() || got.Activity != workspace.ActivityWorking || got.Background != 1 {
		t.Errorf("pane status %q activity %q background %d: status must stay idle, activity working", got.Status, got.Activity, got.Background)
	}
	for _, pr := range st.Projects {
		if pr.Active && pr.Activity != workspace.ActivityWorking {
			t.Errorf("project activity = %q, want working", pr.Activity)
		}
	}
	if len(st.Tabs) > 0 && st.Tabs[0].Attention {
		t.Error("a tab with only background work is marked for attention")
	}

	p.Sess.SetBackground(nil)
	st = nextState(t, conn, func(s stateMsg) bool { return s.Working == 0 })
	if st.Working != 0 || projectWorking(st) != 0 {
		t.Errorf("top bar working = %d, project working = %d once it ended, want 0", st.Working, projectWorking(st))
	}
}
