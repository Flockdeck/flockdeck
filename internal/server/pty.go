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

	// Everything printed so far, so the window can rebuild its screen after a
	// reload or reconnect.
	if len(replay) > 0 {
		if err := writeChunk(ctx, conn, replay); err != nil {
			return
		}
	}

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
			if err := writeChunk(ctx, conn, chunk); err != nil {
				return
			}
		}
	}
}

func writeChunk(ctx context.Context, conn *websocket.Conn, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageBinary, data)
}
