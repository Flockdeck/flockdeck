package server

import (
	"bytes"
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
// does not have to sit through half a minute of quiet; a server reads them once,
// as it is made, into its own fields.
//
// The answer is given as long as a frame of output is given to arrive. A ping
// can go out behind a frame already on its way, and a window allowed less for
// the one than the other was dropped for a frame it was still taking.
var (
	pingInterval = 30 * time.Second
	pingTimeout  = writeBudget
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
	// A window reached through the relay is somebody using the pane from their
	// phone, from its opening to its closing; see relayUse.
	relay := fromRemote(r)
	if relay {
		relayUse.mark(id, time.Now())
		defer func() { relayUse.mark(id, time.Now()) }()
		// The desk is told this pane's terminal has a phone open on it, and
		// again once this socket closes -- see remoteViewersFor.
		device := r.Header.Get("Flockdeck-Remote-Device")
		remoteTermViewers.add(id, viewer, device)
		s.Wake()
		defer func() {
			remoteTermViewers.remove(id, viewer)
			s.Wake()
		}()
	}
	defer func() {
		// This window is no longer one the pane is sized for, so it goes back
		// to the window used before it, or to what the rest can all show. Handed
		// over rather than waited on: letting go of a connection should not
		// queue behind whatever the workspace is busy with.
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
	// Armed when a stream starts afresh on a full-screen program; see
	// armRepaint.
	var repaint atomic.Bool
	go s.applyResizes(ctx, id, measured, &repaint)
	go s.readInput(ctx, cancel, conn, id, viewer, relay, measured, &live)
	var writes writeGauge
	go keepalive(ctx, cancel, conn, &writes, s.pingInterval, s.pingTimeout)

	for {
		if sess != nil {
			var (
				subID  int
				replay []byte
				out    <-chan []byte
			)
			fresh := true
			if resume {
				var start int64
				var resumed bool
				subID, replay, start, resumed, out = sess.SubscribeFrom(epoch, from)
				fresh = !resumed
				// Only the run the window was watching can be carried on from.
				// One that replaces it after a restart starts from nothing.
				from = -1
				h := streamHeader{Epoch: sess.Epoch(), Offset: start, Resumed: resumed, End: start + int64(len(replay))}
				if err := writeHeader(ctx, conn, &writes, h); err != nil {
					if subID >= 0 {
						sess.Unsubscribe(subID)
					}
					return
				}
			} else {
				subID, replay, out = sess.Subscribe()
			}
			s.armRepaint(ctx, &repaint, id, viewer, sess, fresh)
			ended := streamOutput(ctx, conn, replay, out, liveFrame(r), &writes)
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
			if err := writeChunk(ctx, conn, &writes, termReset); err != nil {
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
//
// A socket that is being written to is not pinged. Output still getting
// through is answer enough that the window is there, and a ping sent then goes
// out behind the frame on its way: a replay's frame can take a phone on a slow
// link most of the write budget, and the ping waiting it out had the window
// dropped part way through the replay, to reconnect and be sent the same
// replay again, and never get past it. Nothing is lost by it, because writes
// hold themselves to account: one that stalls runs out of its budget and
// drops the connection anyway.
func keepalive(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, writes *writeGauge, interval, timeout time.Duration) {
	defer cancel()
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if writes.writingWithin(interval) {
				continue
			}
			before := writes.finished.Load()
			pingCtx, done := context.WithTimeout(ctx, timeout)
			err := conn.Ping(pingCtx)
			done()
			// A frame that started just after the look above holds the ping
			// up all the same, and one that got through while the ping
			// waited says as much as its answer would have.
			if err != nil && ctx.Err() == nil && (writes.busy.Load() > 0 || writes.finished.Load() != before) {
				continue
			}
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
func (s *Server) readInput(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, id string, viewer int64, relay bool, measured chan<- struct{}, live *atomic.Pointer[session.Session]) {
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
			if !isTerminalReply(data) && viewers.touch(id, viewer) {
				nudge()
			}
			if relay && !isTerminalReply(data) {
				relayUse.mark(id, time.Now())
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
		if ctl.Focus && relay {
			relayUse.mark(id, time.Now())
		}
		if ctl.Resize != nil {
			viewers.set(id, viewer, ctl.Resize.Cols, ctl.Resize.Rows)
			nudge()
		}
	}
}

// isTerminalReply reports whether input is a terminal answering its program
// rather than somebody typing.
//
// A program asks the terminal things -- what it is, where the cursor is, what
// colour the background is -- and a terminal told to report focus says when it
// gains and loses it. The terminal in every window watching the pane answers,
// each through its own socket, so counting the answers as use would hand the
// pane to whichever window's answer happened to arrive last. Shift+F3 and a
// cursor report can be the same bytes; that one key not counting is the price.
func isTerminalReply(p []byte) bool {
	if len(p) < 3 || p[0] != 0x1b {
		return false
	}
	switch p[1] {
	case ']', 'P', '_', '^':
		// Answers to colour queries and requests for settings.
		return true
	case '[':
		switch p[len(p)-1] {
		case 'R', 'c', 'n', 't':
			// Cursor position, device attributes, status, window reports.
			return true
		case 'I', 'O':
			// Focus gained and lost.
			return len(p) == 3
		case 'y':
			// A mode's state.
			return bytes.Contains(p, []byte("$"))
		}
	}
	return false
}

// applyResizes takes what this window has measured to the goroutine that owns
// the workspace -- and, the first time after a stream has started afresh on a
// full-screen program, has the pane redraw for it (armRepaint).
func (s *Server) applyResizes(ctx context.Context, id string, measured <-chan struct{}, repaint *atomic.Bool) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-measured:
			s.do(func() {
				s.fitPane(id)
				if repaint.CompareAndSwap(true, false) {
					s.repaintPane(id)
				}
			})
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

// altScreen reports whether a pane's program is on the alternate screen. It is
// a variable so a test need not run a full-screen program to be on one.
var altScreen = (*session.Session).AltScreen

// repaintResize applies one step of a repaint. It is a variable so a test can
// see the steps without a program there to redraw.
var repaintResize = func(s *Server, id string, cols, rows int) {
	s.ws.ResizePaneTerminal(id, cols, rows)
}

// repaintGap is how long a pane is left a row short before it is put back, so
// its program sees a change rather than two that cancel out.
var repaintGap = 100 * time.Millisecond

// armRepaint readies a repaint for a stream that has just started afresh on a
// full-screen program.
//
// A window attaching to a pane that shows vim, htop or an agent's own
// full-screen view is sent the replay with the terminal modes put back -- and
// the replay of a full-screen program is its screen as it happened to be drawn,
// a piece at a time, so the window shows garbage until the program next
// redraws of its own accord. A program redraws when its terminal changes size,
// so once this window's size is known the pane is made a row shorter and put
// back. A stream that resumes keeps the screen it had and needs none, and nor
// does a program on the ordinary screen, whose replay is simply its output.
//
// It happens once, from whichever comes second: this, or the window's first
// size being applied (applyResizes) -- or, for a window that has not said what
// size it is by unsizedRepaintWait, then, at the size the pane already is.
// The relay's phone client is one of those: it reports a size only when asked
// to fit the pane to its screen, and without this it was left looking at the
// garbled replay for as long as the program had nothing new to draw.
//
// That wait belongs to the connection, whose ctx this is. A window that goes
// before it is over is not repainted for: the pane would drop a row and come
// back in every other window watching it, for a window nobody is looking at.
func (s *Server) armRepaint(ctx context.Context, repaint *atomic.Bool, id string, viewer int64, sess *session.Session, fresh bool) {
	repaint.Store(fresh && altScreen(sess))
	if viewers.sized(id, viewer) && repaint.CompareAndSwap(true, false) {
		go s.do(func() { s.repaintPane(id) })
		return
	}
	if repaint.Load() {
		timer := repaintAfter(unsizedRepaintWait, func() {
			if ctx.Err() == nil && repaint.CompareAndSwap(true, false) {
				s.do(func() { s.repaintPane(id) })
			}
		})
		context.AfterFunc(ctx, func() { timer.Stop() })
	}
}

// unsizedRepaintWait is how long a fresh stream onto a full-screen program
// waits for its window's size before the pane is repainted without it. It is
// a variable so a test need not wait it out, or can wait for ever.
var unsizedRepaintWait = 500 * time.Millisecond

// repaintAfter starts that wait. It is a variable so a test can say when the
// wait is over rather than sit through it.
var repaintAfter = time.AfterFunc

// repaintPane makes a pane a row shorter and, a moment later, puts it back. It
// must run on the workspace goroutine.
func (s *Server) repaintPane(id string) {
	cols, rows := s.repaintSize(id)
	if cols <= 0 || rows < 2 {
		return
	}
	repaintResize(s, id, cols, rows-1)
	time.AfterFunc(repaintGap, func() {
		s.do(func() {
			// Whatever size is right by then, in case a window has changed
			// shape in the meantime.
			if cols, rows := s.repaintSize(id); cols > 0 && rows > 0 {
				repaintResize(s, id, cols, rows)
			}
		})
	})
}

// repaintSize is the size a repaint puts a pane back to: the one its windows
// have measured, or, where none of them has said, the size it is running at.
// It must run on the workspace goroutine.
func (s *Server) repaintSize(id string) (cols, rows int) {
	if cols, rows := viewers.size(id); cols > 0 && rows > 0 {
		return cols, rows
	}
	if p := s.ws.Pane(id); p != nil && p.Sess != nil {
		return p.Sess.Size()
	}
	return 0, 0
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
	deadline := time.After(s.paneLookup)
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
// Live output is merged into frames of at most frame bytes: see liveFrame.
// Every frame is counted on writes, for keepalive.
//
// It returns true when the session's stream ended, leaving the connection
// usable, and false when the connection itself went away.
func streamOutput(ctx context.Context, conn *websocket.Conn, replay []byte, out <-chan []byte, frame int, writes *writeGauge) bool {
	// The replay goes out a frame at a time, and each frame has the write's
	// budget to itself. Sent whole, half a megabyte of history on a slow link
	// -- a phone reaching this through the relay -- outlasted that budget, the
	// socket was dropped for it, and the window reconnected to be sent the same
	// replay again, never once getting as far as the live output.
	for len(replay) > 0 {
		n := min(len(replay), replayFrame)
		if err := writeChunk(ctx, conn, writes, replay[:n]); err != nil {
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
			buf, ended = coalesce(ctx, buf[:0], chunk, out, minFrameGap-time.Since(sent), frame)
			// Merging stops once the frame is full, but the read that filled
			// it can take it past the bound, so what it holds goes out in
			// frames no larger.
			for rest := buf; len(rest) > 0; {
				n := min(len(rest), frame)
				if err := writeChunk(ctx, conn, writes, rest[:n]); err != nil {
					return false
				}
				rest = rest[n:]
			}
			sent = time.Now()
			if ended {
				return true
			}
		}
	}
}

// liveFrame is the largest frame of live output one terminal socket is sent.
//
// A window reached through the relay gets frames no larger than a replay's.
// It is the window most likely to be a phone on a slow link, and a busy pane's
// merged frame of coalesceLimit had the same fifteen seconds to arrive in as a
// replay frame a quarter of its size -- so the burst a replay's bound was
// chosen to survive got the socket dropped instead, and the window reconnected
// to be sent the whole history again.
func liveFrame(r *http.Request) int {
	if fromRemote(r) {
		return replayFrame
	}
	return coalesceLimit
}

const (
	// coalesceLimit bounds one frame. Merging is only worth doing up to the
	// point where the frame itself is the thing the window waits on: past this
	// the remainder goes out as the next frame, which the terminal draws just
	// as happily. A window reached through the relay has a smaller bound: see
	// liveFrame.
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

// coalesce appends chunk, and whatever else is queued behind it, to buf, until
// it holds limit bytes or more.
//
// Everything already produced is taken without waiting. If wait is positive
// and there is room left in the frame, it then gathers for that long, which is
// what paces a pane printing faster than a window can draw.
//
// ended reports that the stream closed while draining, which the caller must
// still act on -- after sending what was collected, since those bytes are the
// last thing the process printed.
func coalesce(ctx context.Context, buf, chunk []byte, out <-chan []byte, wait time.Duration, limit int) (data []byte, ended bool) {
	buf = append(buf, chunk...)
	for len(buf) < limit {
		select {
		case next, ok := <-out:
			if !ok {
				return buf, true
			}
			buf = append(buf, next...)
		default:
			return gather(ctx, buf, out, wait, limit)
		}
	}
	return buf, false
}

// gather waits out the rest of the frame gap, collecting whatever the pane
// prints meanwhile. A frame that is already full does not wait: volume is
// dealt with by the size bound, and pacing is for frequency.
func gather(ctx context.Context, buf []byte, out <-chan []byte, wait time.Duration, limit int) ([]byte, bool) {
	if wait <= 0 || len(buf) >= limit {
		return buf, false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for len(buf) < limit {
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

// sized reports whether one window has said what size it is.
func (v *viewerSizes) sized(pane string, viewer int64) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	st := v.panes[pane][viewer]
	return st.cols > 0 && st.rows > 0
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
	// End is where the replay ends: the bytes from Offset up to here were
	// printed before this window connected. Its terminal answers the questions
	// in them -- what it is, where its cursor is -- as though they had just
	// been asked, and the window keeps those answers from the program.
	End int64 `json:"end"`
}

// writeHeader sends a streamHeader, as the only text frame a terminal socket
// ever carries.
func writeHeader(ctx context.Context, conn *websocket.Conn, writes *writeGauge, h streamHeader) error {
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	return writes.write(ctx, conn, websocket.MessageText, data)
}

func writeChunk(ctx context.Context, conn *websocket.Conn, writes *writeGauge, data []byte) error {
	return writes.write(ctx, conn, websocket.MessageBinary, data)
}

// writeBudget is how long one frame down a terminal socket has to arrive.
const writeBudget = 15 * time.Second

// writeGauge follows one terminal socket's writes, so that keepalive can tell
// a window still taking its output from one that has gone.
type writeGauge struct {
	// busy is how many frames are being written now, and finished how many
	// ever have been; last is when the latest of them finished, in Unix
	// nanoseconds.
	busy     atomic.Int32
	finished atomic.Int64
	last     atomic.Int64
}

// write sends one frame within the write budget. A nil gauge counts nothing.
func (g *writeGauge) write(ctx context.Context, conn *websocket.Conn, typ websocket.MessageType, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, writeBudget)
	defer cancel()
	if g == nil {
		return conn.Write(writeCtx, typ, data)
	}
	// Counted as finished before it stops being busy, so there is no moment
	// at which a frame that went out looks like nothing at all.
	g.busy.Add(1)
	defer g.busy.Add(-1)
	err := conn.Write(writeCtx, typ, data)
	if err == nil {
		g.last.Store(time.Now().UnixNano())
		g.finished.Add(1)
	}
	return err
}

// writingWithin reports whether a frame is being written now, or one finished
// less than d ago.
func (g *writeGauge) writingWithin(d time.Duration) bool {
	return g.busy.Load() > 0 || time.Since(time.Unix(0, g.last.Load())) < d
}
