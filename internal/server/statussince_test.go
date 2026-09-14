package server

import (
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestPaneStatusSinceRidesTheStatePush covers the field the phone's chat view
// times "Working for 3m" from (chat-actions): StatusSince, RFC 3339, next to
// a pane's Status/Detail on the state push, and moved forward whenever the
// status itself changes -- see convo-protocol.md.
func TestPaneStatusSinceRidesTheStatePush(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	id := firstPane(t, srv, ws)

	before := time.Now()
	p, _ := ask(srv, func() *workspace.Pane { return ws.Pane(id) })
	p.Sess.SetStatusFull(session.StatusWorking, "Bash", "")

	st := nextState(t, conn, func(s stateMsg) bool {
		pv, ok := s.Panes[id]
		return ok && pv.Status == "working"
	})
	pv := st.Panes[id]
	if pv.StatusSince == "" {
		t.Fatal("statusSince is empty, want an RFC 3339 timestamp")
	}
	since, err := time.Parse(time.RFC3339, pv.StatusSince)
	if err != nil {
		t.Fatalf("statusSince = %q, not RFC 3339: %v", pv.StatusSince, err)
	}
	if since.Before(before.Truncate(time.Second)) {
		t.Errorf("statusSince = %v, want it no earlier than the status change (%v)", since, before)
	}

	// A later status change moves it forward again.
	time.Sleep(1100 * time.Millisecond)
	p.Sess.SetStatusFull(session.StatusIdle, "", "")
	st = nextState(t, conn, func(s stateMsg) bool {
		pv, ok := s.Panes[id]
		return ok && pv.Status == "idle"
	})
	pv2 := st.Panes[id]
	since2, err := time.Parse(time.RFC3339, pv2.StatusSince)
	if err != nil {
		t.Fatalf("statusSince = %q, not RFC 3339: %v", pv2.StatusSince, err)
	}
	if !since2.After(since) {
		t.Errorf("statusSince = %v, want it later than the first status's %v", since2, since)
	}
}
