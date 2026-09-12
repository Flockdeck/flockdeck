package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/session"
)

// ptyControl is the JSON a window sends on a terminal connection for anything
// that is not keystrokes.
type ptyControl struct {
	Resize *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"resize,omitempty"`
	// Focus says the terminal in this window has just been focused or tapped:
	// somebody is using the pane here. See viewers.
	Focus bool `json:"focus,omitempty"`
}

// restartPoll is how often a terminal socket whose process has ended looks to
// see whether the pane has been given a new one.
const restartPoll = 400 * time.Millisecond

// paneLookup is how long a look at a pane waits on the workspace goroutine
// before giving up and reporting that it learned nothing. It is a variable so
// a test does not have to hold the workspace for the whole of it.
var paneLookup = 5 * time.Second

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
	// sess may be nil for a pane that exists: one whose process never started,
	// because the `claude` CLI was not on PATH. That is not a reason to turn
	// the window away -- doing so had it redial the pane every few seconds for
	// as long as it was open, which on a machine without Claude Code installed
	// is every agent pane there is. The socket waits for a process instead.
	sess, found, err := s.paneSession(id)
	if err != nil {
		http.Error(w, "the workspace is busy", http.StatusServiceUnavailable)
		return
	}
	if !found {
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
	//
	// Through the relay the terminal is compressed, if the browser offers, for
	// the reason the control socket is: the window is often a phone on a
	// metered link, and terminal output -- escape sequences, diffs, redrawn
	// lines -- deflates to a third even a frame at a time. Each frame is
	// compressed on its own, which costs no memory held per socket, and
	// matters here where one window holds a socket for every pane; a keystroke's
	// echo is too small to be compressed at all, so typing is no slower.
	var opts *websocket.AcceptOptions
	if fromRemote(r) {
		opts = &websocket.AcceptOptions{CompressionMode: websocket.CompressionNoContextTakeover}
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	conn.SetReadLimit(4 << 20)
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	viewer := nextViewer.Add(1)
	defer func() {
		// This window is no longer one the pane has to fit inside, so it is
		// free to grow back to whatever the rest can show. Handed over rather
		// than waited on: letting go of a connection should not queue behind
		// whatever the workspace is busy with.
		if viewers.drop(id, viewer) {
			go s.do(func() { s.fitPane(id) })
		}
	}()

	// A window that says what it already holds -- which run of the pane, and
	// how much of its output -- is sent only what it is missing, and keeps the
	// screen and scrollback it has. It asks by sending from=, and is answered
	// with a streamHeader ahead of each run's bytes. A window that does not ask
	// is served exactly as windows always were.
	q := r.URL.Query()
	resume := q.Has("from")
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	epoch, _ := strconv.ParseInt(q.Get("epoch"), 10, 64)

	// Keystrokes and resizes have to reach whichever session the pane is
	// running now, which is no longer the one this connection started on once
	// the pane has been restarted.
	var live atomic.Pointer[session.Session]
	live.Store(sess)
	measured := make(chan struct{}, 1)
	go s.applyResizes(ctx, id, measured)
	go s.readInput(ctx, cancel, conn, id, viewer, measured, &live)
	go keepalive(ctx, cancel, conn)

	for {
		if sess != nil {
			var (
				subID  int
				replay []byte
				out    <-chan []byte
			)
			if resume {
				var start int64
				var resumed bool
				subID, replay, start, resumed, out = sess.SubscribeFrom(epoch, from)
				// Only the run the window was watching can be carried on from.
				// One that replaces it after a restart starts from nothing.
				from = -1
				h := streamHeader{Epoch: sess.Epoch(), Offset: start, Resumed: resumed}
				if err := writeHeader(ctx, conn, h); err != nil {
					if subID >= 0 {
						sess.Unsubscribe(subID)
					}
					return
				}
			} else {
				subID, replay, out = sess.Subscribe()
			}
			ended := streamOutput(ctx, conn, replay, out)
			if subID >= 0 {
				sess.Unsubscribe(subID)
			}
			if !ended {
				// The window went away; there is nothing left to serve.
				return
			}
			if !sess.Exited() {
				// This viewer fell too far behind and was dropped. Hanging up
				// is the recovery: the window reconnects and rebuilds its
				// screen from the history rather than carrying on with a hole
				// in it.
				_ = conn.Close(websocket.StatusNormalClosure, "stream ended")
				return
			}
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
		// for itself while a restart still cost it a reconnection. A pane that
		// never started anything has nothing to undo.
		if sess != nil {
			if err := writeChunk(ctx, conn, termReset); err != nil {
				return
			}
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
var termReset = []byte("\x1bc")

// readInput forwards what the window sends: keystrokes as binary frames,
// everything else as JSON control messages.
func (s *Server) readInput(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, id string, viewer int64, measured chan<- struct{}, live *atomic.Pointer[session.Session]) {
	defer cancel()
	// A size is recorded here and applied elsewhere. Applying it means reaching
	// the workspace goroutine, which can be busy for seconds at a time opening
	// a project, and waiting for that here would stop this window's keystrokes
	// dead behind a measurement. The nudge says only that something changed,
	// so one waiting is as good as ten.
	nudge := func() {
		select {
		case measured <- struct{}{}:
		default:
		}
	}
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			// Typing into a pane whose process has ended, or never had one, is
			// not worth dropping the connection over: the pane may be started
			// under it, and the next keystroke will land.
			if sess := live.Load(); sess != nil {
				_, _ = sess.Write(data)
			}
			// Typing here is using the pane here, which is what it is sized
			// for; a refit is asked for only when that moves it to this window.
			if viewers.touch(id, viewer) {
				nudge()
			}
			continue
		}
		var ctl ptyControl
		if json.Unmarshal(data, &ctl) != nil {
			continue
		}
		if ctl.Focus && viewers.touch(id, viewer) {
			nudge()
		}
		if ctl.Resize != nil {
			viewers.set(id, viewer, ctl.Resize.Cols, ctl.Resize.Rows)
			nudge()
		}
	}
}

// applyResizes takes what this window has measured to the goroutine that owns
// the workspace.
func (s *Server) applyResizes(ctx context.Context, id string, measured <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-measured:
			s.do(func() { s.fitPane(id) })
		}
	}
}

// fitPane sizes a pane for the windows watching it: see viewers. It must run
// on the workspace goroutine.
//
// The size is read here rather than carried in, so that whichever order these
// reach the workspace in, the size that ends up applied is the one that is
// true now. Carrying it meant a measurement worked out before a window closed
// could be applied after, leaving the pane fitted to a window that had gone --
// and nothing would correct it, because the windows that remain have not
// changed shape and will not report again.
//
// It goes through the workspace rather than straight at the session so the
// pane remembers the size. That is the only place the measurement exists --
// the server has no idea what the font metrics are -- and a pane started or
// restarted without it runs its process at a conventional 80x24 and draws its
// first screen at the wrong width.
func (s *Server) fitPane(id string) {
	if cols, rows := viewers.size(id); cols > 0 && rows > 0 {
		s.ws.ResizePaneTerminal(id, cols, rows)
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
		// Looking before waiting is what makes both answers prompt. A restart
		// puts the new session in place and only then closes the old one, and
		// the old one's output is drained for a moment after that, so by the
		// time this is reached the replacement is usually already there. A
		// pane that was closed rather than restarted is already gone too, and
		// this connection has no reason to outlive it by a poll.
		switch sess, found, err := s.paneSession(id); {
		case err != nil:
			// Nothing was learned. Ask again rather than hang up on a pane
			// that is very likely still there and only waiting for a
			// workspace that is busy with something else.
		case !found:
			return nil
		case sess != nil && sess != old:
			return sess
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.closed:
			return nil
		case <-tick.C:
		}
	}
}

// errNoAnswer means the workspace goroutine did not answer in time, so nothing
// at all was learned about the pane. It is not the same as the pane being gone,
// and concluding that from it would hang up on a pane that is still there.
var errNoAnswer = errors.New("the workspace is not answering")

// paneSession reads a pane's current session on the workspace goroutine, which
// is the only place it is safe to look: restarting a pane clears the field and
// then replaces it.
//
// found reports whether the pane still exists at all, which is what separates
// one whose process failed to start from one that has been closed. sess is nil
// for the former.
func (s *Server) paneSession(id string) (sess *session.Session, found bool, err error) {
	type result struct {
		sess  *session.Session
		found bool
	}
	done := make(chan result, 1)
	// One budget covers handing the question over and getting the answer, as
	// waiting to hand it over has no deadline of its own and the queue in
	// front of the workspace fills up exactly when the workspace is slow.
	deadline := time.After(paneLookup)
	select {
	case s.cmds <- func() {
		if p := s.ws.Pane(id); p != nil {
			done <- result{p.Sess, true}
			return
		}
		done <- result{}
	}:
	case <-s.closed:
		return nil, false, errNoAnswer
	case <-deadline:
		return nil, false, errNoAnswer
	}
	select {
	case r := <-done:
		return r.sess, r.found, nil
	case <-s.closed:
		return nil, false, errNoAnswer
	case <-deadline:
		return nil, false, errNoAnswer
	}
}

// streamOutput sends a pane's output down one terminal socket: the replay
// first, so the window can rebuild its screen, then everything the process
// prints from here on.
//
// It returns true when the session's stream ended, leaving the connection
// usable, and false when the connection itself went away.
func streamOutput(ctx context.Context, conn *websocket.Conn, replay []byte, out <-chan []byte) bool {
	// The replay goes out a frame at a time, and each frame has the write's
	// budget to itself. Sent whole, half a megabyte of history on a slow link
	// -- a phone reaching this through the relay -- outlasted that budget, the
	// socket was dropped for it, and the window reconnected to be sent the same
	// replay again, never once getting as far as the live output.
	for len(replay) > 0 {
		n := min(len(replay), replayFrame)
		if err := writeChunk(ctx, conn, replay[:n]); err != nil {
			return false
		}
		replay = replay[n:]
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

	// replayFrame bounds one frame of a replay. What decides it is the
	// slowest link a window is used over rather than the cost of a frame: a
	// write has fifteen seconds, so a frame of this size needs no more than a
	// few kilobytes a second to arrive in time.
	replayFrame = 64 << 10

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

// viewers remembers, for each pane, the size each attached window has reported
// and when each was last used, and nextViewer names them apart.
//
// More than one window can be looking at the same pane: a second launch
// attaches to the running instance rather than starting a rival, the page can
// be opened in a browser beside the application's own window, and a phone can
// reach it through the relay. Each measures its own geometry, so each reports
// a different size, and the pane can only be one of them.
//
// It follows the window somebody is using -- the last one typed into, or whose
// terminal was focused or tapped -- as tmux's window-size "latest" does.
// Fitting every window at once meant a phone opened for a glance reflowed every
// desktop terminal it looked at to phone width until it was closed. Reporting
// a size is not using a window: every window reports one on connecting and on
// each fit, and opening one must not take the pane over. Until any window has
// been used the pane takes the least of each dimension, which every one of
// them can draw; when the one in use goes, the pane follows the one used
// before it.
//
// This lives here rather than on the Server because it is the terminal
// transport's own bookkeeping, and it holds nothing once the last window
// showing a pane has gone.
var (
	viewers    = &viewerSizes{panes: map[string]map[int64]viewerState{}}
	nextViewer atomic.Int64
)

// viewerState is what one window has said about a pane: the size it measured,
// and when it was last used, as a place in viewerSizes.seq -- zero for never.
type viewerState struct {
	cols, rows int
	used       int64
}

type viewerSizes struct {
	mu    sync.Mutex
	panes map[string]map[int64]viewerState
	// seq orders uses: the later a window was used, the higher its number.
	seq int64
}

// set records what one window measured for a pane.
func (v *viewerSizes) set(pane string, viewer int64, cols, rows int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	byViewer := v.panes[pane]
	if byViewer == nil {
		byViewer = map[int64]viewerState{}
		v.panes[pane] = byViewer
	}
	st := byViewer[viewer]
	st.cols, st.rows = cols, rows
	byViewer[viewer] = st
}

// touch records that somebody used a pane in one window, and reports whether
// that moved the pane to it -- whether a refit is worth asking for.
func (v *viewerSizes) touch(pane string, viewer int64) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	byViewer := v.panes[pane]
	if byViewer == nil {
		byViewer = map[int64]viewerState{}
		v.panes[pane] = byViewer
	}
	was := lastUsed(byViewer)
	v.seq++
	st := byViewer[viewer]
	st.used = v.seq
	byViewer[viewer] = st
	return lastUsed(byViewer) != was
}

// lastUsed is the window most recently used, among those that have measured
// the pane, or zero when none of them has been used.
//
// A window that has never said how big it is cannot be sized for, and using
// one leaves the pane as it is. The relay's phone client is one: it reports a
// size only when asked to fit the pane to its screen, and a glance from it
// must not shrink the desk's terminal to the least of the rest.
func lastUsed(byViewer map[int64]viewerState) int64 {
	var last, at int64
	for id, st := range byViewer {
		if st.used > at && st.cols > 0 && st.rows > 0 {
			last, at = id, st.used
		}
	}
	return last
}

// drop forgets a window that has gone away, and reports whether anything is
// still watching the pane -- which is whether it is worth being resized for.
func (v *viewerSizes) drop(pane string, viewer int64) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	byViewer := v.panes[pane]
	if _, had := byViewer[viewer]; !had {
		return false
	}
	delete(byViewer, viewer)
	if len(byViewer) == 0 {
		delete(v.panes, pane)
		return false
	}
	return true
}

// size is the size the pane should be: the window last used's, or, until one
// has been, the least any of them can show, taken per dimension. It is zero
// when nothing has measured the pane.
func (v *viewerSizes) size(pane string) (cols, rows int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	byViewer := v.panes[pane]
	if last := lastUsed(byViewer); last != 0 {
		return byViewer[last].cols, byViewer[last].rows
	}
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

// streamHeader opens each run's stream for a window that asked to resume: which
// run of the pane the bytes after it belong to, where in that run's output they
// begin, and whether they carry on from what the window already holds or it
// has to start its terminal afresh. The window counts the bytes it is sent from
// Offset, and that count is what it sends back as from= when it reconnects.
type streamHeader struct {
	Epoch   int64 `json:"epoch"`
	Offset  int64 `json:"offset"`
	Resumed bool  `json:"resumed"`
}

// writeHeader sends a streamHeader, as the only text frame a terminal socket
// ever carries.
func writeHeader(ctx context.Context, conn *websocket.Conn, h streamHeader) error {
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

func writeChunk(ctx context.Context, conn *websocket.Conn, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageBinary, data)
}
