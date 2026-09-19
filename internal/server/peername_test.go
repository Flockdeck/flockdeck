package server

import "testing"

// TestPeerNameReachesTheStatePush checks that a pane's own report of the
// name another Claude session would address it by -- set the same way the
// hook server's handler sets it, via workspace.SetPanePeerName -- reaches
// the window in the pane's own view, the same wire field the header badge
// reads.
func TestPeerNameReachesTheStatePush(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	id := firstPane(t, srv, ws)
	nextState(t, conn, nil)

	if !ws.SetPanePeerName(id, "flockdeck-8d") {
		t.Fatal("could not set the peer name")
	}
	st := nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].PeerName != "" })
	if st.Panes[id].PeerName != "flockdeck-8d" {
		t.Errorf("peerName = %q, want flockdeck-8d", st.Panes[id].PeerName)
	}
}
