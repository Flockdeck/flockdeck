package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// nextNotice reads control messages until a one-off notice arrives, which is
// how a refused drop reaches the window.
func nextNotice(t *testing.T, conn *websocket.Conn) noticeMsg {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var msg noticeMsg
		if json.Unmarshal(data, &msg) == nil && msg.Type == "notice" {
			return msg
		}
	}
	t.Fatal("timed out waiting for a notice")
	return noticeMsg{}
}

// TestDragPaneAcrossASplit drives the commands a drag sends: the pane the user
// picked up ends on the side of the pane they dropped it on, and its process is
// still the one that was there.
func TestDragPaneAcrossASplit(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	st := nextState(t, conn, nil)
	first := st.Tabs[0].Root.Pane

	sendCmd(t, conn, command{Cmd: "splitPane", ID: first, Dir: "h", Kind: "shell"})
	st = nextState(t, conn, func(s stateMsg) bool {
		return s.Tabs[0].Root != nil && len(s.Tabs[0].Root.Children) == 2
	})
	second := st.Tabs[0].Root.Children[1].Pane

	// Drag the right-hand pane onto the bottom edge of the left-hand one: the
	// row becomes a column holding both.
	sendCmd(t, conn, command{Cmd: "movePane", ID: second, Target: first, Edge: "bottom"})
	st = nextState(t, conn, func(s stateMsg) bool {
		r := s.Tabs[0].Root
		return r != nil && r.Dir == "v" && len(r.Children) == 2 &&
			r.Children[0].Pane == first && r.Children[1].Pane == second
	})
	if len(st.Panes) != 2 {
		t.Fatalf("panes = %d, want both still running", len(st.Panes))
	}
	if st.Tabs[0].Focus != second {
		t.Errorf("focus = %q, want the pane that was dragged", st.Tabs[0].Focus)
	}

	// Dropping one on the middle of the other exchanges them in place.
	sendCmd(t, conn, command{Cmd: "swapPanes", ID: second, Target: first})
	nextState(t, conn, func(s stateMsg) bool {
		r := s.Tabs[0].Root
		return r != nil && len(r.Children) == 2 &&
			r.Children[0].Pane == second && r.Children[1].Pane == first
	})
}

// TestDragPaneOutToItsOwnTab covers dropping a pane on the new-tab button, and
// dragging it back into the tab it came from.
func TestDragPaneOutToItsOwnTab(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	st := nextState(t, conn, nil)
	first := st.Tabs[0].Root.Pane
	firstTab := st.Tabs[0].ID

	sendCmd(t, conn, command{Cmd: "splitPane", ID: first, Dir: "h", Kind: "shell"})
	st = nextState(t, conn, func(s stateMsg) bool {
		return s.Tabs[0].Root != nil && len(s.Tabs[0].Root.Children) == 2
	})
	second := st.Tabs[0].Root.Children[1].Pane

	sendCmd(t, conn, command{Cmd: "movePaneToNewTab", ID: second})
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs) == 2 })
	if st.Tabs[0].Root.Pane != first {
		t.Errorf("the original tab should be left with %q, got %+v", first, st.Tabs[0].Root)
	}
	if st.Tabs[1].Root.Pane != second {
		t.Errorf("the new tab should hold %q, got %+v", second, st.Tabs[1].Root)
	}
	if len(st.Panes) != 2 {
		t.Errorf("panes = %d, want neither destroyed by the move", len(st.Panes))
	}

	// Dropping it back onto the first tab in the tab bar returns it.
	sendCmd(t, conn, command{Cmd: "movePaneToTab", ID: second, Target: firstTab})
	nextState(t, conn, func(s stateMsg) bool {
		return len(s.Tabs) == 1 && s.Tabs[0].Root != nil && len(s.Tabs[0].Root.Children) == 2
	})
}

// TestDragTabAlongTheBar covers reordering the tab strip.
func TestDragTabAlongTheBar(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	nextState(t, conn, nil)
	sendCmd(t, conn, command{Cmd: "newTab", Kind: "shell", Text: "second"})
	st := nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs) == 2 })

	firstID, secondID := st.Tabs[0].ID, st.Tabs[1].ID
	sendCmd(t, conn, command{Cmd: "moveTab", ID: secondID, Target: firstID})
	st = nextState(t, conn, func(s stateMsg) bool {
		return len(s.Tabs) == 2 && s.Tabs[0].ID == secondID
	})
	if st.Tabs[1].ID != firstID {
		t.Errorf("tab order = %v, want the dragged tab first", []string{st.Tabs[0].ID, st.Tabs[1].ID})
	}
}

// TestDropTabOnTabMergesThem covers the drop in the middle of another tab: the
// two tabs become one holding both agents, and neither is restarted.
func TestDropTabOnTabMergesThem(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	st := nextState(t, conn, nil)
	first := st.Tabs[0].Root.Pane
	firstTab := st.Tabs[0].ID

	sendCmd(t, conn, command{Cmd: "newTab", Kind: "shell", Text: "second"})
	st = nextState(t, conn, func(s stateMsg) bool { return len(s.Tabs) == 2 })
	secondTab := st.Tabs[1].ID
	second := st.Tabs[1].Root.Pane

	sendCmd(t, conn, command{Cmd: "mergeTab", ID: secondTab, Target: firstTab, Dir: "h"})
	st = nextState(t, conn, func(s stateMsg) bool {
		r := len(s.Tabs) == 1
		return r && s.Tabs[0].Root != nil && len(s.Tabs[0].Root.Children) == 2
	})
	if st.Tabs[0].ID != firstTab {
		t.Errorf("tab = %q, want the one merged into %q", st.Tabs[0].ID, firstTab)
	}
	root := st.Tabs[0].Root
	if root.Children[0].Pane != first || root.Children[1].Pane != second {
		t.Errorf("panes = %+v, want both side by side", root.Children)
	}
	if len(st.Panes) != 2 {
		t.Errorf("panes = %d, want neither stopped by the merge", len(st.Panes))
	}
	if st.Tabs[0].Focus != second {
		t.Errorf("focus = %q, want the pane from the tab that was dragged", st.Tabs[0].Focus)
	}

	// Nothing is left to merge in once they are all in one tab.
	sendCmd(t, conn, command{Cmd: "mergeAllTabs", ID: firstTab, Dir: "h"})
	if msg := nextNotice(t, conn); !msg.Error {
		t.Errorf("notice = %+v, want it flagged as an error", msg)
	}
}

// TestImpossibleMoveIsReportedNotApplied checks that a drop aimed at something
// that has since gone comes back as a notice rather than changing the layout.
func TestImpossibleMoveIsReportedNotApplied(t *testing.T) {
	srv, _ := newTestServer(t)
	conn := dialControl(t, srv)

	st := nextState(t, conn, nil)
	only := st.Tabs[0].Root.Pane

	sendCmd(t, conn, command{Cmd: "movePane", ID: only, Target: "gone", Edge: "left"})
	if msg := nextNotice(t, conn); !msg.Error {
		t.Errorf("notice = %+v, want it flagged as an error", msg)
	}
}
