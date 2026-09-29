package server

import (
	"fmt"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// TestPeerNameIsReadSafelyWhileReported checks that the window's snapshot can
// be built while a pane reports its peer name. The name is set by the hook
// server's goroutines and read by the workspace goroutine as it builds every
// snapshot, so it must be read in a way that is safe against the write; run
// with -race to see it.
func TestPeerNameIsReadSafelyWhileReported(t *testing.T) {
	srv, ws := newTestServer(t)
	id := srv.firstPaneID(t)
	hookSrv := ws.HookServer()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 200 {
			if err := hooks.PeerName(hookSrv.BaseURL(), hookSrv.Token(), id, fmt.Sprintf("peer-%d", i)); err != nil {
				t.Errorf("peer name: %v", err)
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	for {
		ask(srv, func() stateMsg { return srv.snapshot() })
		select {
		case <-done:
			return
		default:
		}
	}
}
