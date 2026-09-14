package server

import "testing"

// The state a phone or a window reads says this instance understands
// mutePane, and carries muted:true for a pane that has been muted -- which
// mutePane again with muted:false clears.
func TestMutePaneCommandSetsStateAndCapability(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)
	if !st.CanMutePane {
		t.Error("the state does not say mutePane is understood")
	}
	id := firstPane(t, srv, ws)
	if st.Panes[id].Muted {
		t.Fatal("a pane started out muted")
	}

	sendCmd(t, conn, command{Cmd: "mutePane", ID: id, Muted: true})
	st = nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].Muted })
	if !st.Panes[id].Muted {
		t.Fatal("mutePane did not mark the pane muted")
	}

	sendCmd(t, conn, command{Cmd: "mutePane", ID: id, Muted: false})
	st = nextState(t, conn, func(s stateMsg) bool { return !s.Panes[id].Muted })
	if st.Panes[id].Muted {
		t.Fatal("mutePane with muted:false did not unmute the pane")
	}
}

// A mutePane for a pane that has since closed is refused by name, exactly as
// closePane and restartPane are.
func TestMutePaneForAGoneIDRefuses(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "mutePane", ID: "a-pane-that-has-gone", Muted: true})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error || note.Text != paneGone {
		t.Errorf("mutePane for a gone pane = %+v, want an error notice %q", note, paneGone)
	}
}
