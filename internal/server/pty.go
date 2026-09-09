package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
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

// pingInterval is how often an idle terminal socket is checked, and
// pingTimeout how long the answer is waited for. They are variables so a test
// does not have to sit through half a minute of quiet.
var (
	pingInterval = 30 * time.Second
	pingTimeout  = 10 * time.Second
)

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

	// No origin patterns: the default is that the page opening this socket
	// must have come from this server's own address, and no wider allowance
	// is wanted. Cookies are shared across the ports of a host and a WebSocket
	// is not subject to CORS, so anything else served from 127.0.0.1 or
	// localhost -- the user's own dev server, or anything that can be made to
	// serve a page from one -- had the token attached to a socket it opened
	// here, and could then type into every agent.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(4 << 20)
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	viewer := nextViewer.Add(1)
	defer func() {
		// This window is no longer one the pane has to fit inside.
		if cols, rows, ok := viewers.drop(id, viewer); ok {
			s.do(func() { s.ws.ResizePaneTerminal(id, cols, rows) })
		}
	}()

	// Keystrokes and resizes have to reach whichever session the pane is
	// running now, which is no longer the one this connection started on once
	// the pane has been restarted.
	var live atomic.Pointer[session.Session]
	live.Store(sess)
	go s.readInput(ctx, cancel, conn, id, viewer, &live)
	go keepalive(ctx, cancel, conn)

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
		// A process that died mid-flight leaves the emulator however it had
		// set it up -- alternate screen, mouse reporting, application cursor
		// keys, an odd character set. The replacement has no idea any of that
		// is on, so it is reset before it draws anything. The window did this
		// for itself while a restart still cost it a reconnection.
		if err := writeChunk(ctx, conn, termReset); err != nil {
			return
		}
		sess = next
		live.Store(sess)
	}
}

// keepalive holds a quiet socket to account.
//
// A terminal socket can be idle for a long time and still be perfectly
// healthy: a pane waiting on its agent prints nothing, and one whose process
// has exited is held open for as long as it takes someone to restart it. In
// neither case does the server touch the connection, so a window that went
// away without saying so -- killed, or with a reader wedged behind an input
// write that will not complete -- is never found out, and the pane's
// subscription, this connection and its poll of the workspace are held for as
// long as the instance runs.
//
// A ping has to be answered by the window's own read loop, so it also tells
// the difference between a window that is merely quiet and one that has
// stopped listening.
func keepalive(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn) {
	defer cancel()
	tick := time.NewTicker(pingInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pingCtx, done := context.WithTimeout(ctx, pingTimeout)
			err := conn.Ping(pingCtx)
			done()
			if err != nil {
				return
			}
		}
	}
}

// termReset is RIS, which puts the emulator back to how it starts up.
var termReset = []byte("c")

// readInput forwards what the window sends: keystrokes as binary frames,
// everything else as JSON control messages.
func (s *Server) readInput(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, id string, viewer int64, live *atomic.Pointer[session.Session]) {
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
			cols, rows := viewers.set(id, viewer, ctl.Resize.Cols, ctl.Resize.Rows)
			// Through the workspace rather than straight at the session, so
			// the pane remembers the size the browser measured. It is the
			// only place that measurement exists -- the server has no idea
			// what the font metrics are -- and a restarted pane that does not
			// have it starts its process at a conventional 80x24 and draws
			// its first screen at the wrong width.
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
	var sent time.Time
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
			buf, ended = coalesce(ctx, buf[:0], chunk, out, minFrameGap-time.Since(sent))
			if err := writeChunk(ctx, conn, buf); err != nil {
				return false
			}
			sent = time.Now()
			if ended {
				return true
			}
		}
	}
}

const (
	// coalesceLimit bounds one frame. Merging is only worth doing up to the
	// point where the frame itself is the thing the window waits on: past this
	// the remainder goes out as the next frame, which the terminal draws just
	// as happily.
	coalesceLimit = 256 << 10

	// minFrameGap is the closest together two frames for one pane are sent.
	//
	// Draining the queue only helps when there is a queue, and a pane that
	// prints steadily and fast -- an agent streaming its answer a token at a
	// time, a spinner, a progress bar -- never builds one on a loopback
	// socket: each small write is delivered before the next arrives, so it
	// costs a frame of its own. Hundreds a second reach the window, which
	// cannot draw more than its display refreshes anyway, and every one of
	// them is a parse and a render it does not need.
	//
	// So output arriving within this of the last frame waits for the rest of
	// the gap and leaves with whatever else turns up. It is shorter than a
	// refresh at any ordinary rate, so nothing is on screen later than it
	// would have been; and a keystroke echoed into a quiet pane is not
	// affected at all, because the gap has long since passed.
	minFrameGap = 8 * time.Millisecond
)

// coalesce appends chunk, and whatever else is queued behind it, to buf.
//
// Everything already produced is taken without waiting. If wait is positive
// and there is room left in the frame, it then gathers for that long, which is
// what paces a pane printing faster than a window can draw.
//
// ended reports that the stream closed while draining, which the caller must
// still act on -- after sending what was collected, since those bytes are the
// last thing the process printed.
func coalesce(ctx context.Context, buf, chunk []byte, out <-chan []byte, wait time.Duration) (data []byte, ended bool) {
	buf = append(buf, chunk...)
	for len(buf) < coalesceLimit {
		select {
		case next, ok := <-out:
			if !ok {
				return buf, true
			}
			buf = append(buf, next...)
		default:
			return gather(ctx, buf, out, wait)
		}
	}
	return buf, false
}

// gather waits out the rest of the frame gap, collecting whatever the pane
// prints meanwhile. A frame that is already full does not wait: volume is
// dealt with by the size bound, and pacing is for frequency.
func gather(ctx context.Context, buf []byte, out <-chan []byte, wait time.Duration) ([]byte, bool) {
	if wait <= 0 || len(buf) >= coalesceLimit {
		return buf, false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for len(buf) < coalesceLimit {
		select {
		case next, ok := <-out:
			if !ok {
				return buf, true
			}
			buf = append(buf, next...)
		case <-timer.C:
			return buf, false
		case <-ctx.Done():
			return buf, false
		}
	}
	return buf, false
}

// viewers remembers the size each attached window has reported for each pane,
// and nextViewer names them apart.
//
// More than one window can be looking at the same pane: a second launch
// attaches to the running instance rather than starting a rival, and the page
// can be opened in a browser beside the application's own window. Each
// measures its own geometry, so each reports a different size, and whichever
// spoke last used to win. The loser was left with a terminal wider than it
// could draw -- every line wrapped -- and no reason to ever report again,
// because nothing about its own geometry had changed.
//
// This lives here rather than on the Server because it is the terminal
// transport's own bookkeeping, and it holds nothing once the last window
// showing a pane has gone.
var (
	viewers    = &viewerSizes{panes: map[string]map[int64]termSize{}}
	nextViewer atomic.Int64
)

type termSize struct{ cols, rows int }

type viewerSizes struct {
	mu    sync.Mutex
	panes map[string]map[int64]termSize
}

// set records what one window measured and returns the size the pane should
// actually be: the smallest any of its windows can show, taken per dimension,
// which is the only size all of them draw correctly.
func (v *viewerSizes) set(pane string, viewer int64, cols, rows int) (int, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	byViewer := v.panes[pane]
	if byViewer == nil {
		byViewer = map[int64]termSize{}
		v.panes[pane] = byViewer
	}
	byViewer[viewer] = termSize{cols, rows}
	return smallest(byViewer, cols, rows)
}

// drop forgets a window that has gone away and reports the size the pane is
// free to grow back to, if anything is still watching it.
func (v *viewerSizes) drop(pane string, viewer int64) (cols, rows int, ok bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	byViewer := v.panes[pane]
	if _, had := byViewer[viewer]; !had {
		return 0, 0, false
	}
	delete(byViewer, viewer)
	if len(byViewer) == 0 {
		delete(v.panes, pane)
		return 0, 0, false
	}
	cols, rows = smallest(byViewer, 0, 0)
	return cols, rows, true
}

func smallest(byViewer map[int64]termSize, cols, rows int) (int, int) {
	for _, sz := range byViewer {
		if sz.cols > 0 && (cols <= 0 || sz.cols < cols) {
			cols = sz.cols
		}
		if sz.rows > 0 && (rows <= 0 || sz.rows < rows) {
			rows = sz.rows
		}
	}
	return cols, rows
}

func writeChunk(ctx context.Context, conn *websocket.Conn, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageBinary, data)
}
