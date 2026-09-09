package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// ptyControl is the JSON a window sends on a terminal connection for anything
// that is not keystrokes.
type ptyControl struct {
	Resize *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"resize,omitempty"`
}

// handlePTY streams one pane's terminal: process output down, keystrokes up.
//
// Bytes are passed through untouched in both directions. The browser runs the
// terminal emulator, so it produces correctly encoded input for whatever modes
// the application has enabled, and renders the output itself.
func (s *Server) handlePTY(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p := s.paneByID(r.URL.Query().Get("id"))
	if p == nil || p.Sess == nil {
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

	sess := p.Sess
	subID, replay, out := sess.Subscribe()
	if subID >= 0 {
		defer sess.Unsubscribe(subID)
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Input: keystrokes as binary frames, everything else as JSON.
	go func() {
		defer cancel()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := sess.Write(data); err != nil {
					return
				}
				continue
			}
			var ctl ptyControl
			if json.Unmarshal(data, &ctl) != nil {
				continue
			}
			if ctl.Resize != nil {
				sess.Resize(ctl.Resize.Cols, ctl.Resize.Rows)
			}
		}
	}()

	streamOutput(ctx, conn, replay, out)
}

// streamOutput sends a pane's output down one terminal socket: the replay
// first, so the window can rebuild its screen, then everything the process
// prints from here on.
func streamOutput(ctx context.Context, conn *websocket.Conn, replay []byte, out <-chan []byte) {
	if len(replay) > 0 {
		if err := writeChunk(ctx, conn, replay); err != nil {
			return
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
			return
		case chunk, ok := <-out:
			if !ok {
				// The process exited, or this viewer fell too far behind. The
				// window reconnects and replays either way.
				_ = conn.Close(websocket.StatusNormalClosure, "stream ended")
				return
			}
			var ended bool
			buf, ended = coalesce(buf[:0], chunk, out)
			if err := writeChunk(ctx, conn, buf); err != nil {
				return
			}
			if ended {
				_ = conn.Close(websocket.StatusNormalClosure, "stream ended")
				return
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
