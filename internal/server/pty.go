package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/perch/internal/session"
)

// ptyControl is the JSON a window sends on a terminal connection for anything
// that is not keystrokes.
type ptyControl struct {
	Resize *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"resize,omitempty"`
}

// restartPoll is how often a terminal socket whose process has ended looks to
// see whether the pane has been given a new one.
const restartPoll = 400 * time.Millisecond

// handlePTY streams one pane's terminal: process output down, keystrokes up.
//
// Bytes are passed through untouched in both directions. The browser runs the
// terminal emulator, so it produces correctly encoded input for whatever modes
// the application has enabled, and renders the output itself.
//
// The socket outlives the process in the pane. Restarting an agent replaces
// the session behind the pane, and this follows it, so a restart costs no
// reconnection and an exited pane is not sat there being redialled.
func (s *Server) handlePTY(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.URL.Query().Get("id")
	sess, _ := s.paneSession(id)
	if sess == nil {
		http.Error(w, "no such pane", http.StatusNotFound)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"127.0.0.1:*", "localhost:*"},
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(4 << 20)
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Keystrokes and resizes have to reach whichever session the pane is
	// running now, which is no longer the one this connection started on once
	// the pane has been restarted.
	var live atomic.Pointer[session.Session]
	live.Store(sess)
	go s.readInput(ctx, cancel, conn, id, &live)

	for {
		subID, replay, out := sess.Subscribe()
		ended := streamOutput(ctx, conn, replay, out)
		if subID >= 0 {
			sess.Unsubscribe(subID)
		}
		if !ended {
			// The window went away; there is nothing left to serve.
			return
		}
		if !sess.Exited() {
			// This viewer fell too far behind and was dropped. Hanging up is
			// the recovery: the window reconnects and rebuilds its screen from
			// the history rather than carrying on with a hole in it.
			_ = conn.Close(websocket.StatusNormalClosure, "stream ended")
			return
		}
		next := s.waitForRestart(ctx, id, sess)
		if next == nil {
			_ = conn.Close(websocket.StatusNormalClosure, "pane closed")
			return
		}
		sess = next
		live.Store(sess)
	}
}

// readInput forwards what the window sends: keystrokes as binary frames,
// everything else as JSON control messages.
func (s *Server) readInput(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, id string, live *atomic.Pointer[session.Session]) {
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		sess := live.Load()
		if sess == nil {
			continue
		}
		if typ == websocket.MessageBinary {
			// Typing into a pane whose process has ended is not worth dropping
			// the connection over: the pane may be restarted under it, and the
			// next keystroke will land.
			_, _ = sess.Write(data)
			continue
		}
		var ctl ptyControl
		if json.Unmarshal(data, &ctl) != nil {
			continue
		}
		if ctl.Resize != nil {
			// Through the workspace rather than straight at the session, so
			// the pane remembers the size the browser measured. It is the
			// only place that measurement exists -- the server has no idea
			// what the font metrics are -- and a restarted pane that does not
			// have it starts its process at a conventional 80x24 and draws
			// its first screen at the wrong width.
			cols, rows := ctl.Resize.Cols, ctl.Resize.Rows
			s.do(func() { s.ws.ResizePaneTerminal(id, cols, rows) })
		}
	}
}

// waitForRestart waits for the pane to be given a new process. It returns nil
// once the pane is gone, or the window is.
//
// Polling is what is available: nothing announces a pane being restarted. It
// is cheap next to what it replaces, which was the window redialling an exited
// pane every quarter second and replaying its whole history into a freshly
// cleared terminal each time.
func (s *Server) waitForRestart(ctx context.Context, id string, old *session.Session) *session.Session {
	tick := time.NewTicker(restartPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.closed:
			return nil
		case <-tick.C:
			sess, ok := s.paneSession(id)
			if !ok {
				return nil
			}
			if sess != nil && sess != old {
				return sess
			}
		}
	}
}

// paneSession reads a pane's current session on the workspace goroutine, which
// is the only place it is safe to look: restarting a pane clears the field and
// then replaces it. ok reports whether the pane still exists at all, which is
// what separates one mid-restart from one that has been closed.
func (s *Server) paneSession(id string) (sess *session.Session, ok bool) {
	type result struct {
		sess *session.Session
		ok   bool
	}
	done := make(chan result, 1)
	s.do(func() {
		if p := s.ws.Pane(id); p != nil {
			done <- result{p.Sess, true}
			return
		}
		done <- result{}
	})
	select {
	case r := <-done:
		return r.sess, r.ok
	case <-s.closed:
		return nil, false
	case <-time.After(5 * time.Second):
		return nil, false
	}
}

// streamOutput sends a pane's output down one terminal socket: the replay
// first, so the window can rebuild its screen, then everything the process
// prints from here on.
//
// It returns true when the session's stream ended, leaving the connection
// usable, and false when the connection itself went away.
func streamOutput(ctx context.Context, conn *websocket.Conn, replay []byte, out <-chan []byte) bool {
	if len(replay) > 0 {
		if err := writeChunk(ctx, conn, replay); err != nil {
			return false
		}
	}

	// A burst -- a build's output, a page of scrollback, Claude redrawing --
	// reaches the session as many small reads, and one websocket frame per
	// read is where the cost of it lands. Whatever has already queued behind
	// the first chunk goes out with it, so a burst costs a handful of frames
	// rather than hundreds, and the subscriber queue drains fast enough that
	// the viewer is not dropped for falling behind in the middle of one.
	var buf []byte
	for {
		select {
		case <-ctx.Done():
			return false
		case chunk, ok := <-out:
			if !ok {
				// The process exited, or this viewer fell too far behind.
				return true
			}
			var ended bool
			buf, ended = coalesce(buf[:0], chunk, out)
			if err := writeChunk(ctx, conn, buf); err != nil {
				return false
			}
			if ended {
				return true
			}
		}
	}
}

// coalesceLimit bounds one frame. Merging is only worth doing up to the point
// where the frame itself is the thing the window waits on: past this the
// remainder goes out as the next frame, which the terminal draws just as
// happily.
const coalesceLimit = 256 << 10

// coalesce appends chunk, and whatever else the session has already queued, to
// buf. It never waits for more: only output that has already been produced is
// merged, so nothing is held back to see whether more arrives.
//
// ended reports that the stream closed while draining, which the caller must
// still act on -- after sending what was collected, since those bytes are the
// last thing the process printed.
func coalesce(buf, chunk []byte, out <-chan []byte) (data []byte, ended bool) {
	buf = append(buf, chunk...)
	for len(buf) < coalesceLimit {
		select {
		case next, ok := <-out:
			if !ok {
				return buf, true
			}
			buf = append(buf, next...)
		default:
			return buf, false
		}
	}
	return buf, false
}

func writeChunk(ctx context.Context, conn *websocket.Conn, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageBinary, data)
}
