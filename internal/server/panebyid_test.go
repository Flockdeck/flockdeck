package server

import "testing"

// TestControlActsOnAPaneInAnyTab covers closePane, restartPane and toggleZoom
// sent for a pane in a tab other than the one on screen. These used to go
// through focusFor, which only ever finds a pane in the tab already on
// screen -- so a helper sitting in a tab nobody had switched to was refused,
// "that pane is no longer open", even though it plainly was.
func TestControlActsOnAPaneInAnyTab(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)
	firstTab := st.Tabs[0].ID

	sendCmd(t, conn, command{Cmd: "newTab", Kind: "shell", Text: "helper"})
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs) == 2 })
	helperTab := st.Tabs[1].ID
	helperPane := st.Tabs[1].Root.Pane

	// Back to the first tab, so the helper's tab is the one not on screen.
	sendCmd(t, conn, command{Cmd: "selectTab", ID: firstTab})
	nextState(t, conn, func(s stateMsg) bool { return s.ActiveTab == firstTab })

	// toggleZoom on the background pane zooms its own tab, and leaves the one
	// on screen alone.
	sendCmd(t, conn, command{Cmd: "toggleZoom", ID: helperPane})
	st = nextState(t, conn, func(s stateMsg) bool {
		for _, tb := range s.Tabs {
			if tb.ID == helperTab {
				return tb.Zoom
			}
		}
		return false
	})
	if st.ActiveTab != firstTab {
		t.Errorf("toggling zoom on a background pane switched the active tab to %q", st.ActiveTab)
	}

	// restartPane on the background pane restarts it in place.
	sendCmd(t, conn, command{Cmd: "restartPane", ID: helperPane})
	st = nextState(t, conn, func(s stateMsg) bool {
		_, ok := s.Panes[helperPane]
		return ok
	})
	if st.ActiveTab != firstTab {
		t.Errorf("restarting a background pane switched the active tab to %q", st.ActiveTab)
	}

	// closePane on the background pane closes it -- and its now-empty tab --
	// leaving the tab on screen exactly where it was.
	sendCmd(t, conn, command{Cmd: "closePane", ID: helperPane})
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs) == 1 })
	if st.ActiveTab != firstTab {
		t.Errorf("closing a background pane switched the active tab to %q", st.ActiveTab)
	}
	if _, ok := st.Panes[helperPane]; ok {
		t.Error("the background pane is still in the snapshot after being closed")
	}
}

// TestClosePaneForAGonePaneStillRefuses covers the other side of the same
// fix: a pane that really has gone must still be refused by name, not
// silently accepted now that closing looks in every tab rather than only the
// one on screen.
func TestClosePaneForAGonePaneStillRefuses(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "closePane", ID: "a-pane-that-has-gone"})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error || note.Text != paneGone {
		t.Errorf("closePane for a gone pane = %+v, want an error notice %q", note, paneGone)
	}
}
