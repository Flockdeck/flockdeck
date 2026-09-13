package server

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// TestNothingQueuedRunsOnceTheServerHasClosed covers the shutdown saving the
// workspace and then closing it. Closing the server only closed a channel: the
// change the workspace goroutine was applying ran on, and with more queued
// behind it, its select took the closed channel or the next change at random
// -- so changes went on being made to the workspace while it was being saved
// and closed. Nothing queued runs once Close has returned, and Stopped waits
// for the change that was already running.
//
// It is run many times over, since what went wrong was a coin tossed once per
// change.
func TestNothingQueuedRunsOnceTheServerHasClosed(t *testing.T) {
	for round := 0; round < 50; round++ {
		s := &Server{
			http:     &http.Server{},
			cmds:     make(chan func(), 64),
			closed:   make(chan struct{}),
			loopDone: make(chan struct{}),
		}
		go s.runLoop()

		inFlight, release := make(chan struct{}), make(chan struct{})
		s.do(func() { close(inFlight); <-release })
		<-inFlight
		var ran atomic.Int32
		for i := 0; i < 20; i++ {
			s.do(func() { ran.Add(1) })
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}

		select {
		case <-s.Stopped():
			t.Fatal("the workspace was let go of with a change still being applied to it")
		default:
		}
		close(release)
		select {
		case <-s.Stopped():
		case <-time.After(5 * time.Second):
			t.Fatal("the workspace goroutine never stopped")
		}
		if n := ran.Load(); n != 0 {
			t.Fatalf("round %d: %d changes queued before the server closed were applied after it had", round, n)
		}
	}
}
