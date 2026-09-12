package server

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// TestForgettingARecentProjectWaitsItsTurn covers the × beside a recent
// project. Forgetting it is a read and a rewrite of the whole list, and so is
// opening or switching to a project, which happens on the workspace goroutine;
// run side by side, one could put back what the other had just taken out, and
// a project dropped from the list came back. The forget now waits its turn
// there.
func TestForgettingARecentProjectWaitsItsTurn(t *testing.T) {
	srv, _ := newTestServer(t)
	gone := t.TempDir()
	if err := store.TouchRecent(gone); err != nil {
		t.Fatal(err)
	}
	listed := func() bool {
		list, err := store.Recents()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range list {
			if p.Root == filepath.Clean(gone) {
				return true
			}
		}
		return false
	}

	// The workspace goroutine, busy with something of its own.
	busy, release := make(chan struct{}), make(chan struct{})
	srv.do(func() { close(busy); <-release })
	<-busy

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.handleCommand(&controlClient{out: make(chan []byte, 8)}, command{Cmd: "forgetRecent", Root: gone})
	}()
	time.Sleep(300 * time.Millisecond)
	if !listed() {
		t.Fatal("the list was rewritten beside the workspace goroutine's own writes to it, not in turn with them")
	}

	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forgetting never finished")
	}
	if listed() {
		t.Fatal("the project was not forgotten")
	}
}
