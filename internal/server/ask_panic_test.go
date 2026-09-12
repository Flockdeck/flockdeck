package server

import (
	"testing"
	"time"
)

// TestAskReturnsWhenTheWorkspacePanics covers a question to the workspace
// goroutine whose answer panics. The goroutine recovers and tells every window
// something went wrong, but the answer was never sent, and ask waited for it
// until the server closed -- and many handlers ask from a window's own read
// loop, so that window went on looking connected while ignoring every command.
func TestAskReturnsWhenTheWorkspacePanics(t *testing.T) {
	srv, _ := newTestServer(t)

	returned := make(chan bool, 1)
	go func() {
		_, ok := ask(srv, func() int { panic("a closure that goes wrong") })
		returned <- ok
	}()
	select {
	case ok := <-returned:
		if ok {
			t.Error("a question whose answer panicked was reported as answered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ask never returned after the workspace's answer panicked")
	}

	// And the workspace goroutine carries on answering.
	if v, ok := ask(srv, func() int { return 7 }); !ok || v != 7 {
		t.Fatalf("the next question was answered %d, %v; want 7, true", v, ok)
	}
}
