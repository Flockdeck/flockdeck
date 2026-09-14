package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// sameDevices reports whether got holds exactly want, in any order -- what a
// pane's remoteViewers is compared against below, since remoteViewersFor's
// own order is nothing a test should depend on.
func sameDevices(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

// waitForRemoteViewers polls srv.remoteViewersFor until it matches want or
// the deadline passes. A single read would be fine on the machine this was
// written on, but not on a slower one, or under Linux's coarser scheduling --
// registering a viewer happens on the connection's own goroutine, not on the
// call that opened it.
func waitForRemoteViewers(t *testing.T, srv *Server, paneID string, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got []string
	for {
		got = srv.remoteViewersFor(paneID)
		if sameDevices(got, want...) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("remoteViewersFor(%s) = %v, want %v", paneID, got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// hasRemoteViewer is a stateWithin condition matching a pane whose
// remoteViewers holds exactly the device names given.
func hasRemoteViewer(paneID string, want ...string) func(stateMsg) bool {
	return func(s stateMsg) bool {
		v, ok := s.Panes[paneID]
		if !ok {
			return false
		}
		return sameDevices(v.RemoteViewers, want...)
	}
}

// dialRemoteControlDevice opens the control socket through the tunnel as a
// phone would, naming the device the way the relay does.
func dialRemoteControlDevice(ts *httptest.Server, origin, device string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", origin)
	h.Set("Flockdeck-Remote-Device", device)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/control",
		&websocket.DialOptions{HTTPHeader: h})
	if err == nil {
		conn.SetReadLimit(16 << 20)
	}
	return conn, err
}

// dialRemotePTYDevice opens a pane's terminal socket through the tunnel, as a
// phone would, naming the device the way the relay does.
func dialRemotePTYDevice(t *testing.T, ts *httptest.Server, paneID, device string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", ts.URL)
	h.Set("Flockdeck-Remote-Device", device)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+paneID,
		&websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("dial pty through the tunnel: %v", err)
	}
	conn.SetReadLimit(16 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

// TestConversationRemoteViewerAppearsAndClears covers the chat half of the
// pane header's phone glyph: a window reached through the relay opening a
// pane's chat view is what should make it show, a window on the desk opening
// the same chat should not, and it should clear again whether the phone asks
// to close the chat or simply goes away.
func TestConversationRemoteViewerAppearsAndClears(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`)

	// A window on the desk opening the same pane's chat is not a phone.
	desk := &controlClient{out: make(chan []byte, 8)}
	srv.conversationOpen(desk, paneID, "")
	nextRaw(t, desk)
	if got := srv.remoteViewersFor(paneID); len(got) != 0 {
		t.Fatalf("remoteViewersFor after a local window opened the chat = %v, want none", got)
	}

	phone := &controlClient{out: make(chan []byte, 8), remote: true, device: "Jim's iPhone"}
	srv.conversationOpen(phone, paneID, "")
	nextRaw(t, phone)
	if got := srv.remoteViewersFor(paneID); !sameDevices(got, "Jim's iPhone") {
		t.Fatalf("remoteViewersFor after a phone opened the chat = %v, want [Jim's iPhone]", got)
	}

	// Asked to close: it clears.
	srv.conversationClose(phone, paneID)
	if got := srv.remoteViewersFor(paneID); len(got) != 0 {
		t.Fatalf("remoteViewersFor after the phone closed the chat = %v, want none", got)
	}

	// Reopened, then gone without asking: it clears the same way.
	srv.conversationOpen(phone, paneID, "")
	nextRaw(t, phone)
	if got := srv.remoteViewersFor(paneID); !sameDevices(got, "Jim's iPhone") {
		t.Fatalf("remoteViewersFor after the phone reopened the chat = %v, want [Jim's iPhone]", got)
	}
	srv.convos.dropClient(phone)
	if got := srv.remoteViewersFor(paneID); len(got) != 0 {
		t.Fatalf("remoteViewersFor after the phone disconnected = %v, want none", got)
	}
}

// TestTerminalRemoteViewerAppearsAndClears covers the terminal half: a
// terminal socket opened through the relay makes the pane show a remote
// viewer, one opened locally does not, and closing either way clears it.
func TestTerminalRemoteViewerAppearsAndClears(t *testing.T) {
	srv, ws := newTestServer(t)
	paneID, ok := ask(srv, func() string { return ws.CurrentTab().Focus })
	if !ok || paneID == "" {
		t.Fatal("could not find the test pane")
	}

	local := dialPTY(t, srv, paneID)
	if got := srv.remoteViewersFor(paneID); len(got) != 0 {
		t.Fatalf("remoteViewersFor with only a local terminal open = %v, want none", got)
	}

	ts := remoteServer(t, srv)
	phone := dialRemotePTYDevice(t, ts, paneID, "Jim's iPhone")
	waitForRemoteViewers(t, srv, paneID, "Jim's iPhone")

	// The local terminal is still open and still does not count.
	local.CloseNow()
	waitForRemoteViewers(t, srv, paneID, "Jim's iPhone")

	phone.CloseNow()
	waitForRemoteViewers(t, srv, paneID)
}

// TestRemoteViewersReachTheDesksState covers the point of the whole thing:
// the desk's own window is told, promptly and in the state it already reads
// every pane's header from, when a phone opens this pane -- in its chat view,
// in its terminal, or both, which count as the one phone rather than two --
// and is told again when the phone goes.
func TestRemoteViewersReachTheDesksState(t *testing.T) {
	srv, ws := newTestServer(t)
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	paneID := addAgentPane(t, srv, ws, "claude")
	root := ws.ActiveRoot()
	writeClaudeJSONL(t, claudeTranscriptPath(home, root, paneID),
		`{"type":"user","uuid":"u1","message":{"role":"user","content":"hello"}}`)

	desk := readControl(dialControl(t, srv))
	desk.settle(t)

	ts := remoteServer(t, srv)
	phone, err := dialRemoteControlDevice(ts, ts.URL, "Jim's iPhone")
	if err != nil {
		t.Fatalf("dial through the tunnel: %v", err)
	}
	defer phone.CloseNow()

	sendCmd(t, phone, command{Cmd: "conversationOpen", ID: paneID})
	if _, ok := desk.stateWithin(5*time.Second, hasRemoteViewer(paneID, "Jim's iPhone")); !ok {
		t.Fatal("the desk was not told a phone opened this pane's chat")
	}

	// The same phone also opens the pane's terminal: still the one phone.
	term := dialRemotePTYDevice(t, ts, paneID, "Jim's iPhone")
	if _, ok := desk.stateWithin(5*time.Second, hasRemoteViewer(paneID, "Jim's iPhone")); !ok {
		t.Fatal("the desk lost the phone from this pane once its terminal opened too")
	}

	// The chat closes; the terminal is still open, so the phone is still shown.
	sendCmd(t, phone, command{Cmd: "conversationClose", ID: paneID})
	if _, ok := desk.stateWithin(5*time.Second, hasRemoteViewer(paneID, "Jim's iPhone")); !ok {
		t.Fatal("the desk dropped the phone while its terminal was still open on this pane")
	}

	// The terminal closes too: nothing left watching this pane.
	term.CloseNow()
	if _, ok := desk.stateWithin(5*time.Second, hasRemoteViewer(paneID)); !ok {
		t.Fatal("the desk was not told the phone left this pane")
	}
}
