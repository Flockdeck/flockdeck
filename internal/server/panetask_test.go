package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestPaneTaskReachesTheStatePush checks that a pane's own opening task is
// carried whole to the window, the same wire field the header's tooltip
// reads to say what a pane is doing beyond its bare directory name or its
// tab's own short-cut title.
func TestPaneTaskReachesTheStatePush(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	lead := firstPane(t, srv, ws)
	nextState(t, conn, nil)

	const task = "Investigate why the checkout pane keeps losing its focus after a restart."
	child, ok := ask(srv, func() string {
		id, err := ws.Spawn(lead, workspace.SpawnOptions{Task: task, Kind: session.KindShell})
		if err != nil {
			t.Error(err)
		}
		return id
	})
	if !ok || child == "" {
		t.Fatal("the child pane did not start")
	}

	st := nextState(t, conn, func(s stateMsg) bool { return s.Panes[child].Task != "" })
	if st.Panes[child].Task != task {
		t.Errorf("task = %q, want %q", st.Panes[child].Task, task)
	}
	// The lead pane was opened with no task of its own, and gets none
	// invented for it.
	if got := st.Panes[lead].Task; got != "" {
		t.Errorf("lead pane's task = %q, want none", got)
	}
}
