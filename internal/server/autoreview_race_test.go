package server

import (
	"strings"
	"sync"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
)

// TestAutoReviewCountIsReadSafelyWhileApproving checks that the window's
// snapshot can be built while auto-review is approving a pane's calls. The
// count of approvals is kept by the hook server's goroutines and read by the
// workspace goroutine as it builds every snapshot, so it must be read in a
// way that is safe against the write; run with -race to see it.
func TestAutoReviewCountIsReadSafelyWhileApproving(t *testing.T) {
	t.Setenv(hooks.LaunchEnv, "") // see TestAutoReviewAllowsAReadOnlyCommand
	srv, ws := newTestServer(t)
	id := srv.firstPaneID(t)
	hookSrv := ws.HookServer()
	ws.SetPaneAutoReview(id, true)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			stdin := strings.NewReader(`{"session_id":"s","tool_name":"Bash","tool_input":{"command":"git status"}}`)
			if _, err := hooks.Emit(stdin, hookSrv.Endpoint(), hookSrv.Token(), id, "PreToolUse"); err != nil {
				t.Errorf("emit: %v", err)
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
