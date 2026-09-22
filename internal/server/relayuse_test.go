package server

import (
	"context"
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
