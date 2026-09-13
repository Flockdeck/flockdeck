package server

import (
	"sync"
	"testing"
	"time"
)

// TestANoticeReachesAWindowStillBeingSetUp covers a notice sent while a
// window's hello is still waiting its turn -- a panic reported the moment a
// window connected, say. Only the state has to wait for the key table, since
// the palette and the hints are drawn beside it; a notice sent to the windows
// that had been greeted alone never reached one in that moment at all.
func TestANoticeReachesAWindowStillBeingSetUp(t *testing.T) {
	srv, _ := newTestServer(t)
	release := make(chan struct{})
	stop := sync.OnceFunc(func() { close(release) })
	t.Cleanup(stop)
	srv.do(func() { <-release })

	conn := dialControl(t, srv)
	for deadline := time.Now().Add(5 * time.Second); srv.ClientCount() != 1; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the window was never counted")
		}
	}
	srv.notifyAll("something went wrong", true)
	stop()

	var note noticeMsg
	readUntil(t, conn, "notice", &note)
	if note.Text != "something went wrong" || !note.Error {
		t.Fatalf("the window was told %+v; want the notice sent while it was being set up", note)
	}
}
