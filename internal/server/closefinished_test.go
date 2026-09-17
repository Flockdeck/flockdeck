package server

import (
	"errors"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestCloseFinishedPanesCommandClosesFinishedPanesAndReportsCounts drives the
// closeFinishedPanes command the way the palette entry and the Agents
// overview's button do: no id, no confirmation, just a report of what it did.
// It also covers a working pane, a waiting pane, and a failed one (StatusIdle
// with Err set, as outcomeOf reads "failed" in the web UI) being left alone.
func TestCloseFinishedPanesCommandClosesFinishedPanesAndReportsCounts(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)
	firstPaneID := st.Tabs[0].Root.Pane

	idleAgent, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "idle").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusIdle, "")
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}

	exitedShell, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "exited").Tree.Panes()[0])
		p.Sess.SetStatus(session.StatusExited, "")
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}

	waitingAgent, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "waiting").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusWaiting, "")
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}

	failedAgent, ok := ask(srv, func() *workspace.Pane {
		p := ws.Pane(ws.NewTab(session.KindShell, ws.ActiveRoot(), "failed").Tree.Panes()[0])
		p.Kind = session.KindAgent
		p.Sess.SetStatus(session.StatusIdle, "")
		p.Err = errors.New("boom")
		return p
	})
	if !ok {
		t.Fatal("server closed")
	}

	sendCmd(t, conn, command{Cmd: "closeFinishedPanes"})

	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Error {
		t.Fatalf("closeFinishedPanes reported an error: %+v", note)
	}
	const want = "closed 2 finished panes and 2 empty tabs"
	if note.Text != want {
		t.Errorf("notice = %q, want %q", note.Text, want)
	}

	remaining, ok := ask(srv, func() map[string]bool {
		m := map[string]bool{}
		for _, id := range []string{firstPaneID, idleAgent.ID, exitedShell.ID, waitingAgent.ID, failedAgent.ID} {
			m[id] = ws.Pane(id) != nil
		}
		return m
	})
	if !ok {
		t.Fatal("server closed")
	}
	if !remaining[firstPaneID] {
		t.Error("the original pane was closed")
	}
	if remaining[idleAgent.ID] {
		t.Error("the idle agent pane is still open")
	}
	if remaining[exitedShell.ID] {
		t.Error("the exited shell pane is still open")
	}
	if !remaining[waitingAgent.ID] {
		t.Error("the waiting agent pane was closed")
	}
	if !remaining[failedAgent.ID] {
		t.Error("the failed agent pane was closed")
	}
}

// TestCloseFinishedPanesCommandWithNothingToCloseSaysSo checks the no-op case
// still gets a notice, since running with no confirmation means closing
// nothing must not look like the click did nothing.
func TestCloseFinishedPanesCommandWithNothingToCloseSaysSo(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "closeFinishedPanes"})

	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Error {
		t.Fatalf("closeFinishedPanes reported an error with nothing to close: %+v", note)
	}
	if note.Text != "no finished panes to close" {
		t.Errorf("notice = %q, want %q", note.Text, "no finished panes to close")
	}
}
