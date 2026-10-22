package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var barrierSeq atomic.Int64

// readThroughBarrier returns every message the window is sent up to a notice
// that the server sends now. A message a test is checking for the absence of
// has been queued by the time it asks, and a window's messages arrive in the
// order they were queued, so reading until the barrier is the same as reading
// until nothing more could come, without a wait for a fixed time.
//
// It is for a message sent while a call the test made was running (on the
// server's goroutine, say, through ask). It cannot see one a goroutine of its
// own sends later.
func readThroughBarrier(t *testing.T, srv *Server, conn *websocket.Conn) [][]byte {
	t.Helper()
	text := fmt.Sprintf("barrier %d", barrierSeq.Add(1))
	srv.notifyAll(text, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out [][]byte
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for the barrier %q: %v", text, err)
		}
		var note noticeMsg
		if json.Unmarshal(data, &note) == nil && note.Type == "notice" && note.Text == text {
			return out
		}
		out = append(out, data)
	}
}

// shellsHaveStarted waits until no pane in the window's state is still starting,
// then until the connection is quiet. A new shell is reported as starting and
// goes idle after a start-up grace the session package owns; that change is
// broadcast, so a test that wants to know a later push was not caused by it has
// to see it first. Waiting for it replaces waiting for three seconds with
// nothing sent, which took about twice as long.
func (r *controlReader) shellsHaveStarted(t *testing.T) {
	t.Helper()
	if _, ok := r.stateWithin(20*time.Second, func(s stateMsg) bool {
		for _, p := range s.Panes {
			if p.Status == "starting" {
				return false
			}
		}
		return len(s.Panes) > 0
	}); !ok {
		t.Fatal("a pane never stopped starting")
	}
	r.settle(t)
}
