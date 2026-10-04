package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Closing a pane that was touched from the relay leaves nothing behind in
// relayUse: mark's only deletion path used to be since, called only for
// panes still open when a relay push iterates them, so a pane closed after
// being opened from a phone had its entry sit in relayUse.at for the life of
// the process. See Workspace.SetPaneClosedHook and (*Server).PaneClosed.
func TestClosingAPaneForgetsRelayUse(t *testing.T) {
	srv, ws := newTestServer(t)
	id := addPane(t, srv, ws, "second")

	ts := remoteServer(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", ts.URL)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+id, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("open the pane through the relay: %v", err)
	}
	waitFor(t, func() bool { return relayUse.has(id) })
	conn.Close(websocket.StatusNormalClosure, "")

	if ok, _ := ask(srv, func() bool { return ws.ClosePaneByID(id) }); !ok {
		t.Fatal("closing the pane was refused")
	}

	waitFor(t, func() bool { return !relayUse.has(id) })
}

// TestAPaneClosedUnderItsRelaySocketLeavesNoRelayUse covers the other order:
// the pane is closed while the phone's socket to it is still open. The socket
// ends after the pane has gone, and what it did on the way out was to record
// the pane as used, which put back the entry PaneClosed had just removed and
// left it for the life of the process.
func TestAPaneClosedUnderItsRelaySocketLeavesNoRelayUse(t *testing.T) {
	srv, ws := newTestServer(t)
	ts := remoteServer(t, srv)
	for round := range 10 {
		id := addPane(t, srv, ws, fmt.Sprintf("second%d", round))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		h := http.Header{}
		h.Set("Origin", ts.URL)
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+id, &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			t.Fatalf("open the pane through the relay: %v", err)
		}
		waitFor(t, func() bool { return relayUse.has(id) })

		if ok, _ := ask(srv, func() bool { return ws.ClosePaneByID(id) }); !ok {
			t.Fatal("closing the pane was refused")
		}
		// The socket ends on its own once its pane is gone.
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				break
			}
		}
		cancel()
		conn.CloseNow()
		// The server's side of the socket has its own way out after the
		// client has gone, and the entry is looked at once it has been
		// through the workspace goroutine, which it goes through on the way.
		if _, ok := ask(srv, func() bool { return true }); !ok {
			t.Fatal("the workspace did not answer")
		}
		time.Sleep(50 * time.Millisecond)
		if relayUse.has(id) {
			t.Fatalf("round %d: the closed pane %s was left in relayUse", round, id)
		}
	}
}
