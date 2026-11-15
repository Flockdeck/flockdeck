package server

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// otherProjectIdle opens a second project, puts an idle agent pane in it and
// leaves the first project on screen. It says the id of that pane.
func otherProjectIdle(t *testing.T, srv *Server, ws *workspace.Workspace) string {
	t.Helper()
	id, ok := ask(srv, func() string {
		first := ws.ActiveRoot()
		second := t.TempDir()
		if err := ws.OpenProject(second); err != nil {
			t.Error(err)
		}
		p := ws.Pane(ws.NewTab(session.KindShell, second, "elsewhere").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusIdle, "")
		ws.SelectProject(first)
		return p.ID
	})
	if !ok || id == "" {
		t.Fatal("the pane in the other project did not start")
	}
	return id
}

// A pane of another project is refused when named by id, whatever its state, and
// the refusal reads as a pane that is not there.
func TestCloseRefusesAPaneOfAnotherProject(t *testing.T) {
	srv, ws := newTestServer(t)
	caller := firstPane(t, srv, ws)
	other := otherProjectIdle(t, srv, ws)
	hookSrv := ws.HookServer()

	_, err := hooks.Close(hookSrv.BaseURL(), hookSrv.Token(), caller, hooks.CloseRequest{Target: other, Force: true})
	if err == nil || !strings.Contains(err.Error(), "is open here") {
		t.Fatalf("err = %v, want a refusal as for a pane that is not open", err)
	}
	if open, ok := ask(srv, func() bool { return ws.Pane(other) != nil }); !ok || !open {
		t.Error("the pane of the other project was closed")
	}
}

// -finished closes the finished panes of the caller's project only.
func TestCloseFinishedLeavesOtherProjectsAlone(t *testing.T) {
	srv, ws := newTestServer(t)
	caller := firstPane(t, srv, ws)
	other := otherProjectIdle(t, srv, ws)
	mine, ok := ask(srv, func() string {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "mine").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusIdle, "")
		return p.ID
	})
	if !ok {
		t.Fatal("server closed")
	}
	hookSrv := ws.HookServer()

	res, err := hooks.Close(hookSrv.BaseURL(), hookSrv.Token(), caller, hooks.CloseRequest{Finished: true})
	if err != nil {
		t.Fatalf("close -finished: %v", err)
	}
	if res.Panes != 1 {
		t.Errorf("Panes = %d, want 1", res.Panes)
	}
	open, ok := ask(srv, func() map[string]bool {
		return map[string]bool{"mine": ws.Pane(mine) != nil, "other": ws.Pane(other) != nil}
	})
	if !ok {
		t.Fatal("server closed")
	}
	if open["mine"] {
		t.Error("the finished pane of the caller's project is still open")
	}
	if !open["other"] {
		t.Error("-finished closed a pane of another project")
	}
}
