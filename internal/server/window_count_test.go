package server

import (
	"sync"
	"testing"
	"time"
)

// TestAWindowCountsFromTheMomentItConnects covers a window whose socket opens
// while the workspace goroutine is busy -- a page reloaded while a project is
// opening, say. Nothing may be sent to it before its key table, but it is
// there, and the application decides whether to quit by counting the windows
// on this machine: one that counted only once its hello had been handed over
// left a moment in which the last window seemed to have gone, and the
// application quits, with every agent, when that moment outlasts its grace.
func TestAWindowCountsFromTheMomentItConnects(t *testing.T) {
	srv, _ := newTestServer(t)
	release := make(chan struct{})
	stop := sync.OnceFunc(func() { close(release) })
	t.Cleanup(stop)
	srv.do(func() { <-release })

	dialControl(t, srv)
	for deadline := time.Now().Add(5 * time.Second); srv.LocalClientCount() != 1; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("a window that connected while the workspace was busy counts as %d windows, want 1", srv.LocalClientCount())
		}
	}
	if n := srv.ClientCount(); n != 1 {
		t.Errorf("ClientCount = %d, want the one window", n)
	}
	stop()
}
