package server

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestCloseClosesAnIdlePane covers the ordinary case: a helper an agent
// spawned has settled, and asking to close it by id does, with no -force
// needed.
func TestCloseClosesAnIdlePane(t *testing.T) {
	srv, ws := newTestServer(t)
	caller := firstPane(t, srv, ws)
	hookSrv := ws.HookServer()

	idle, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "idle").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusIdle, "")
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}

	res, err := hooks.Close(hookSrv.BaseURL(), hookSrv.Token(), caller, hooks.CloseRequest{Target: idle.ID})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !res.Closed {
		t.Error("Closed = false, want true")
	}
	if remains, ok := ask(srv, func() bool { return ws.Pane(idle.ID) != nil }); !ok || remains {
		t.Error("the idle pane is still open")
	}
}

// TestCloseRefusesABusyPaneWithoutForce covers the safety rule this command
// exists to enforce: naming the wrong id must not be able to cut off work
// still under way, so a pane that is not idle or exited is refused unless
// -force says otherwise.
func TestCloseRefusesABusyPaneWithoutForce(t *testing.T) {
	srv, ws := newTestServer(t)
	caller := firstPane(t, srv, ws)
	hookSrv := ws.HookServer()

	working, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "working").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusWorking, "")
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}

	_, err := hooks.Close(hookSrv.BaseURL(), hookSrv.Token(), caller, hooks.CloseRequest{Target: working.ID})
	if err == nil || !strings.Contains(err.Error(), "still working") {
		t.Fatalf("err = %v, want a refusal naming the pane as still working", err)
	}
	if remains, ok := ask(srv, func() bool { return ws.Pane(working.ID) != nil }); !ok || !remains {
		t.Error("a busy pane was closed with no -force")
	}
}

// TestCloseForceClosesABusyPane covers the deliberate override: an agent that
// really does want a busy pane gone can say so explicitly.
func TestCloseForceClosesABusyPane(t *testing.T) {
	srv, ws := newTestServer(t)
	caller := firstPane(t, srv, ws)
	hookSrv := ws.HookServer()

	working, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "working").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusWorking, "")
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}

	res, err := hooks.Close(hookSrv.BaseURL(), hookSrv.Token(), caller, hooks.CloseRequest{Target: working.ID, Force: true})
	if err != nil {
		t.Fatalf("close -force: %v", err)
	}
	if !res.Closed {
		t.Error("Closed = false, want true")
	}
	if remains, ok := ask(srv, func() bool { return ws.Pane(working.ID) != nil }); !ok || remains {
		t.Error("-force did not close the busy pane")
	}
}

// TestCloseRefusesClosingItself covers the other safety rule: a pane cannot
// use this to end its own process, which would be racing the very reply this
// request is waiting on.
func TestCloseRefusesClosingItself(t *testing.T) {
	srv, ws := newTestServer(t)
	caller := firstPane(t, srv, ws)
	hookSrv := ws.HookServer()

	_, err := hooks.Close(hookSrv.BaseURL(), hookSrv.Token(), caller, hooks.CloseRequest{Target: caller, Force: true})
	if err == nil {
		t.Fatal("a pane was allowed to close itself")
	}
	if remains, ok := ask(srv, func() bool { return ws.Pane(caller) != nil }); !ok || !remains {
		t.Error("the calling pane was closed")
	}
}

// TestCloseRefusesAnUnknownPane checks a stale or mistyped id is reported
// rather than silently doing nothing.
func TestCloseRefusesAnUnknownPane(t *testing.T) {
	srv, ws := newTestServer(t)
	caller := firstPane(t, srv, ws)
	hookSrv := ws.HookServer()

	_, err := hooks.Close(hookSrv.BaseURL(), hookSrv.Token(), caller, hooks.CloseRequest{Target: "no-such-pane"})
	if err == nil || !strings.Contains(err.Error(), "no-such-pane") {
		t.Fatalf("err = %v, want a refusal naming the unknown pane", err)
	}
}

// TestCloseFinishedClosesEveryFinishedPane covers -finished: the same effect
// as the "Close finished panes" command, reached through the hook server
// instead of the control socket, with the same counts coming back.
func TestCloseFinishedClosesEveryFinishedPane(t *testing.T) {
	srv, ws := newTestServer(t)
	caller := firstPane(t, srv, ws)
	hookSrv := ws.HookServer()

	idle, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "idle").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusIdle, "")
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}
	working, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "working").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusWorking, "")
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}

	res, err := hooks.Close(hookSrv.BaseURL(), hookSrv.Token(), caller, hooks.CloseRequest{Finished: true})
	if err != nil {
		t.Fatalf("close -finished: %v", err)
	}
	if res.Panes != 1 || res.Tabs != 1 {
		t.Errorf("res = %+v, want Panes=1 Tabs=1", res)
	}
	remaining, ok := ask(srv, func() map[string]bool {
		return map[string]bool{"idle": ws.Pane(idle.ID) != nil, "working": ws.Pane(working.ID) != nil, "caller": ws.Pane(caller) != nil}
	})
	if !ok {
		t.Fatal("server closed")
	}
	if remaining["idle"] {
		t.Error("the idle pane is still open")
	}
	if !remaining["working"] {
		t.Error("the busy pane was swept up by -finished")
	}
	if !remaining["caller"] {
		t.Error("the calling shell pane was swept up by -finished")
	}
}
