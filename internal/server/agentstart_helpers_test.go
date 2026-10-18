package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// revealAgents is an agent every machine that runs these tests has: `go`,
// given the task as a command it does not know, which says so and exits. The
// pane it ran in stays, which is all a test of where the pane went needs.
const revealAgents = `{"version": 1, "agents": [{"id": "gocli", "name": "Go", "exe": "go",
	"args": [{"value": "{{prompt}}"}]}]}`

// place is the tab on screen and the pane focused in it.
type place struct{ tab, pane string }

// wherePlaced reads where the window is, on the workspace goroutine.
func wherePlaced(t *testing.T, srv *Server, ws *workspace.Workspace) place {
	t.Helper()
	p, ok := ask(srv, func() place {
		tab := ws.CurrentTab()
		if tab == nil {
			return place{}
		}
		return place{tab.ID, tab.Focus}
	})
	if !ok {
		t.Fatal("server closed")
	}
	return p
}

// fanoutServer is a test server that can start the agent in revealAgents.
func fanoutServer(t *testing.T) (*Server, *workspace.Workspace) {
	t.Helper()
	srv, ws := newTestServer(t)
	writeAgents(t, revealAgents)
	ws.ReloadAgents()
	return srv, ws
}
