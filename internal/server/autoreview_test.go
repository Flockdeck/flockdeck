package server

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/store"
)

// The state a phone or a window reads says this instance understands
// autoReview, and carries autoReview:true for a pane that has turned it on --
// which autoReview again with autoReview:false clears.
func TestAutoReviewCommandSetsStateAndCapability(t *testing.T) {
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	st := nextState(t, conn, nil)
	if !st.CanAutoReview {
		t.Error("the state does not say autoReview is understood")
	}
	id := firstPane(t, srv, ws)
	if st.Panes[id].AutoReview {
		t.Fatal("a pane started out with auto-review on")
	}

	sendCmd(t, conn, command{Cmd: "autoReview", ID: id, AutoReview: true})
	st = nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].AutoReview })
	if !st.Panes[id].AutoReview {
		t.Fatal("autoReview did not turn auto-review on")
	}

	sendCmd(t, conn, command{Cmd: "autoReview", ID: id, AutoReview: false})
	st = nextState(t, conn, func(s stateMsg) bool { return !s.Panes[id].AutoReview })
	if st.Panes[id].AutoReview {
		t.Fatal("autoReview with autoReview:false did not turn it back off")
	}
}

// An autoReview for a pane that has since closed is refused by name, exactly
// as mutePane, closePane and restartPane are.
func TestAutoReviewForAGoneIDRefuses(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)

	sendCmd(t, conn, command{Cmd: "autoReview", ID: "a-pane-that-has-gone", AutoReview: true})
	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if !note.Error || note.Text != paneGone {
		t.Errorf("autoReview for a gone pane = %+v, want an error notice %q", note, paneGone)
	}
}

// Toggling auto-review writes the layout at once, not at the next timer tick,
// so a crash right after the toggle keeps it.
func TestAutoReviewToggleSavesTheLayout(t *testing.T) {
	t.Setenv("USERPROFILE", stateTempDir(t))
	srv, ws := newTestServer(t)
	conn := dialControl(t, srv)
	nextState(t, conn, nil)
	id := firstPane(t, srv, ws)

	saved := func() *bool {
		st, err := store.Peek(ws.ActiveRoot())
		if err != nil || st == nil {
			return nil
		}
		var found *bool
		var walk func(n *store.Node)
		walk = func(n *store.Node) {
			if n == nil {
				return
			}
			if n.Pane != nil && n.Pane.ID == id {
				found = n.Pane.AutoReview
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		for _, tb := range st.Tabs {
			walk(tb.Root)
		}
		return found
	}

	sendCmd(t, conn, command{Cmd: "autoReview", ID: id, AutoReview: true})
	nextState(t, conn, func(s stateMsg) bool { return s.Panes[id].AutoReview })
	if v := saved(); v == nil || !*v {
		t.Fatalf("the layout was not saved with auto-review on: %v", v)
	}
	sendCmd(t, conn, command{Cmd: "autoReview", ID: id, AutoReview: false})
	nextState(t, conn, func(s stateMsg) bool { return !s.Panes[id].AutoReview })
	if v := saved(); v == nil || *v {
		t.Fatalf("the layout was not saved with auto-review off: %v", v)
	}
}
