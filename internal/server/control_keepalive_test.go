package server

import (
	"testing"
	"time"
)

// TestAControlWindowThatStoppedAnsweringIsLetGo covers the control socket,
// which the terminal sockets' keepalive did not reach. A window that went away
// without closing stayed on the list of windows -- counted at the desk among
// those open through the relay, and sent every snapshot -- until a write to it
// at last timed out, which with the agents quiet was never.
func TestAControlWindowThatStoppedAnsweringIsLetGo(t *testing.T) {
	defer func(i, o time.Duration) { pingInterval, pingTimeout = i, o }(pingInterval, pingTimeout)
	pingInterval, pingTimeout = 100*time.Millisecond, 300*time.Millisecond

	srv, _ := newTestServer(t)
	// Dialled and then never read from, so it never answers a ping -- which
	// is what a window whose reader has stopped looks like from here.
	_ = dialControl(t, srv)
	waitUntil(t, "the window to be counted", func() bool { return srv.ClientCount() == 1 })
	waitUntil(t, "the window that stopped answering to be let go", func() bool { return srv.ClientCount() == 0 })
}
