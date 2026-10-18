package server

import "testing"

// A message to a pane is never typed into it, so the pane's header carries the
// count of what it has not read, and the count falls when the agent reads.
func TestPaneViewCountsUnreadMessages(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()

	unread := func() int {
		pv, ok := ask(srv, func() paneView { return srv.snapshot().Panes[paneID] })
		if !ok {
			t.Fatal("the workspace did not answer")
		}
		return pv.Unread
	}
	if got := unread(); got != 0 {
		t.Fatalf("a pane with no mail has Unread %d", got)
	}

	for _, text := range []string{"one", "two"} {
		if _, ok := ask(srv, func() int {
			if _, err := ws.UserMessage(root, paneID, text); err != nil {
				t.Errorf("send: %v", err)
			}
			return 0
		}); !ok {
			t.Fatal("the workspace did not answer")
		}
	}
	if got := unread(); got != 2 {
		t.Errorf("Unread = %d, want 2", got)
	}

	msgs, err := ws.Inbox(paneID, false)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("inbox = %+v, %v", msgs, err)
	}
	if _, err := ws.ReadMessage(paneID, msgs[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := unread(); got != 1 {
		t.Errorf("Unread after reading one = %d, want 1", got)
	}
}
