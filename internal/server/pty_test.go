package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/workspace"
)

// streamPair runs streamOutput over a real websocket and hands back the client
// end, so the frames a window would actually receive can be counted.
func streamPair(t *testing.T, replay []byte, out <-chan []byte) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if streamOutput(r.Context(), conn, replay, out) {
			_ = conn.Close(websocket.StatusNormalClosure, "stream ended")
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.SetReadLimit(16 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

// readAllFrames reads until the server closes the stream, returning everything
// that arrived and how many frames it took.
func readAllFrames(t *testing.T, conn *websocket.Conn) (data []byte, frames int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return data, frames
			}
			t.Fatalf("read after %d frames (%d bytes): %v", frames, len(data), err)
		}
		data = append(data, b...)
		frames++
	}
}

// TestReplayIsSentInBoundedFrames covers a window on a slow link. Each write
// has a budget of its own, so a replay sent as one frame is the one write most
// likely to run out of it -- and a window dropped part way through a replay
// reconnects to be sent the whole of it again.
func TestReplayIsSentInBoundedFrames(t *testing.T) {
	replay := make([]byte, 512<<10)
	for i := range replay {
		replay[i] = byte('a' + i%26)
	}
	out := make(chan []byte)
	close(out)
	conn := streamPair(t, replay, out)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var got []byte
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("read after %d bytes: %v", len(got), err)
			}
			break
		}
		if len(b) > replayFrame {
			t.Errorf("a replay frame of %d bytes, want at most %d", len(b), replayFrame)
		}
		got = append(got, b...)
	}
	if !bytes.Equal(got, replay) {
		t.Fatalf("got %d bytes, want %d, equal prefix %d", len(got), len(replay), commonPrefix(got, replay))
	}
}

// TestOutputBurstArrivesInFewFrames is the throughput case. A pane that dumps a
// build log reaches the session as hundreds of small reads; sending one frame
// per read is what makes the window fall behind, so whatever has already queued
// must leave together.
func TestOutputBurstArrivesInFewFrames(t *testing.T) {
	const chunks, size = 400, 1024
	out := make(chan []byte, chunks)
	var want []byte
	for i := range chunks {
		c := bytes.Repeat([]byte{byte('a' + i%26)}, size)
		out <- c
		want = append(want, c...)
	}
	close(out)

	got, frames := readAllFrames(t, streamPair(t, nil, out))

	if !bytes.Equal(got, want) {
		t.Fatalf("got %d bytes, want %d, equal prefix %d",
			len(got), len(want), commonPrefix(got, want))
	}
	// Merging is bounded by coalesceLimit, so the floor is the number of frames
	// that bound implies. Anything close to one frame per chunk means the burst
	// was not merged at all.
	maxFrames := 2*(chunks*size/coalesceLimit) + 2
	if frames > maxFrames {
		t.Errorf("burst of %d chunks arrived in %d frames, want at most %d",
			chunks, frames, maxFrames)
	}
}

// TestStreamSendsTheLastOutputBeforeClosing covers a process that prints and
// immediately exits. The bytes queued behind the final read are the last thing
// it said -- a crash message, a shell's goodbye -- and noticing the closed
// channel while draining must not throw them away.
func TestStreamSendsTheLastOutputBeforeClosing(t *testing.T) {
	out := make(chan []byte, 4)
	out <- []byte("panic: ")
	out <- []byte("something went wrong\r\n")
	out <- []byte("exit status 2\r\n")
	close(out)

	got, _ := readAllFrames(t, streamPair(t, nil, out))

	const want = "panic: something went wrong\r\nexit status 2\r\n"
	if string(got) != want {
		t.Errorf("stream delivered %q, want %q", got, want)
	}
}

// TestStreamSendsReplayFirst covers a reconnecting window: it rebuilds its
// screen from the replay, so that has to arrive ahead of anything live.
func TestStreamSendsReplayFirst(t *testing.T) {
	out := make(chan []byte, 1)
	out <- []byte("live\r\n")
	close(out)

	got, _ := readAllFrames(t, streamPair(t, []byte("replayed\r\n"), out))

	const want = "replayed\r\nlive\r\n"
	if string(got) != want {
		t.Errorf("stream delivered %q, want %q", got, want)
	}
}

// TestStreamStopsWhenCancelled covers the teardown that releases the pane's
// subscription: the connection is torn down by cancelling, and an idle stream
// -- a pane that happens not to be printing anything -- must notice that
// rather than sit on the channel forever.
func TestStreamStopsWhenCancelled(t *testing.T) {
	out := make(chan []byte)
	done := make(chan struct{})
	cancels := make(chan context.CancelFunc, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		defer close(done)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		cancels <- cancel
		streamOutput(ctx, conn, nil, out)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	(<-cancels)()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the output loop is still running after it was cancelled")
	}
}

func commonPrefix(a, b []byte) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// BenchmarkStreamBurst measures a pane emptying its queue down a real
// websocket: the burst is already waiting, as it is when the window has been
// busy elsewhere. frames/op is the number that matters -- every frame is a
// header, a write and a wake-up at the far end.
func BenchmarkStreamBurst(b *testing.B) {
	for _, size := range []int{64, 1024} {
		b.Run(fmt.Sprintf("chunk-%d", size), func(b *testing.B) {
			const chunks = 256
			chunk := bytes.Repeat([]byte{'x'}, size)

			// One server, one connection per iteration; the queue for the
			// next burst is handed over before the dial that picks it up.
			next := make(chan (<-chan []byte), 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				streamOutput(r.Context(), conn, nil, <-next)
			}))
			defer srv.Close()
			url := "ws" + strings.TrimPrefix(srv.URL, "http")

			var frames int
			ctx := context.Background()
			b.SetBytes(int64(chunks * size))
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				out := make(chan []byte, chunks)
				for range chunks {
					out <- chunk
				}
				close(out)
				next <- out
				conn, _, err := websocket.Dial(ctx, url, nil)
				if err != nil {
					b.Fatalf("dial: %v", err)
				}
				conn.SetReadLimit(16 << 20)
				b.StartTimer()

				for read := 0; read < chunks*size; {
					_, data, err := conn.Read(ctx)
					if err != nil {
						b.Fatalf("read: %v", err)
					}
					read += len(data)
					frames++
				}
				b.StopTimer()
				conn.CloseNow()
				b.StartTimer()
			}
			b.ReportMetric(float64(frames)/float64(b.N), "frames/op")
		})
	}
}

// dialPTY opens an authorised terminal socket for a pane.
func dialPTY(t *testing.T, srv *Server, paneID string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx,
		"ws://"+srv.Addr()+"/ws/pty?t="+srv.Token()+"&id="+paneID, nil)
	if err != nil {
		t.Fatalf("dial pty: %v", err)
	}
	conn.SetReadLimit(16 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

// awaitOutput types a line into a terminal socket until its marker comes back,
// which is the only reliable signal that the process behind the pane is
// listening. It returns everything that arrived up to and including it.
func awaitOutput(t *testing.T, conn *websocket.Conn, line, marker string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			if err := conn.Write(ctx, websocket.MessageBinary, []byte(line)); err != nil {
				return
			}
			select {
			case <-done:
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()

	var seen strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read pty waiting for %q: %v\nsaw:\n%s", marker, err, seen.String())
		}
		seen.Write(data)
		if strings.Contains(seen.String(), marker) {
			return seen.String()
		}
	}
}

// dialResumable opens a terminal socket the way the window does now, with
// query saying what it already holds.
func dialResumable(t *testing.T, srv *Server, paneID, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws/pty?t="+srv.Token()+"&id="+paneID+query, nil)
	if err != nil {
		t.Fatalf("dial pty: %v", err)
	}
	conn.SetReadLimit(16 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

// TestTerminalResumeAfterARestartStartsAfresh covers a window cut off while
// its pane was restarted. The place it holds is in the output of a process
// that has gone, so it has to be told to start again, not be handed the new
// process's bytes as though they followed on from the old one's.
func TestTerminalResumeAfterARestartStartsAfresh(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	var h streamHeader
	var held int64
	first := dialResumable(t, srv, paneID, "&from=-1")
	readResumable(t, first, "echo before_restart\r", "before_restart", &h, &held)
	first.CloseNow()
	old := h

	before, _, _ := srv.paneSession(paneID)
	sendCmd(t, ctl, command{Cmd: "restartPane", ID: paneID})
	deadline := time.Now().Add(20 * time.Second)
	for {
		if sess, _, _ := srv.paneSession(paneID); sess != nil && sess != before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane was never restarted")
		}
		time.Sleep(20 * time.Millisecond)
	}

	second := dialResumable(t, srv, paneID, fmt.Sprintf("&from=%d&epoch=%d", held, old.Epoch))
	readResumable(t, second, "echo after_restart\r", "after_restart", &h, &held)
	if h.Resumed || h.Epoch == old.Epoch {
		t.Fatalf("after a restart a window holding the old run's output got %+v, want a fresh start on a new run", h)
	}
}

// readResumable types line into a terminal socket until marker comes back,
// counting the output the way the window does: from the offset in the last
// header, a byte at a time. It returns the output that arrived.
func readResumable(t *testing.T, conn *websocket.Conn, line, marker string, h *streamHeader, held *int64) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			if err := conn.Write(ctx, websocket.MessageBinary, []byte(line)); err != nil {
				return
			}
			select {
			case <-done:
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()

	var seen strings.Builder
	for !strings.Contains(seen.String(), marker) {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read pty waiting for %q: %v\nsaw:\n%s", marker, err, seen.String())
		}
		if typ == websocket.MessageText {
			if err := json.Unmarshal(data, h); err != nil {
				t.Fatalf("a text frame that is not a header: %q", data)
			}
			*held = h.Offset
			continue
		}
		*held += int64(len(data))
		seen.Write(data)
	}
	return seen.String()
}

// TestTerminalResumesWhereItWasCutOff covers a window whose terminal socket
// drops and comes back -- a laptop waking, the relay blinking, or the server
// hanging up on a window that fell behind. It used to reset the terminal and
// be sent the last half megabyte again, losing everything older and where the
// person had scrolled to. Saying what it holds, it is sent only what it
// missed, and keeps the rest.
func TestTerminalResumesWhereItWasCutOff(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	var h streamHeader
	var held int64
	first := dialResumable(t, srv, paneID, "&from=-1")
	readResumable(t, first, "echo resume_one\r", "resume_one", &h, &held)
	if h.Resumed {
		t.Fatal("a window holding nothing was told it was resuming")
	}
	first.CloseNow()

	second := dialResumable(t, srv, paneID, fmt.Sprintf("&from=%d&epoch=%d", held, h.Epoch))
	was := held
	got := readResumable(t, second, "echo resume_two\r", "resume_two", &h, &held)
	if !h.Resumed || h.Offset != was {
		t.Fatalf("reconnecting holding %d bytes got %+v, want a resume from exactly there", was, h)
	}
	if strings.Contains(got, "echo resume_one") {
		t.Errorf("a resume was sent output the window already held:\n%s", got)
	}

	// A window that does not say what it holds is served as windows always
	// were: bytes, and never a header it would not know what to do with.
	legacy := dialPTY(t, srv, paneID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if typ, _, err := legacy.Read(ctx); err != nil || typ != websocket.MessageBinary {
		t.Fatalf("a window that did not ask to resume got a %v frame (%v)", typ, err)
	}
}

// TestTerminalSocketFollowsARestartedPane covers restarting an agent. The pane
// keeps its id and its place on screen, so the terminal socket serving it has
// no reason to be torn down; before this it was, and an exited pane was
// redialled several times a second, replaying its whole history each time.
func TestTerminalSocketFollowsARestartedPane(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	st := nextState(t, ctl, nil)
	paneID := st.Tabs[0].Root.Pane

	pty := dialPTY(t, srv, paneID)
	awaitOutput(t, pty, "echo before_restart\r", "before_restart")

	before, _, _ := srv.paneSession(paneID)
	sendCmd(t, ctl, command{Cmd: "restartPane", ID: paneID})

	// The restart is a command like any other and is applied when the
	// workspace gets to it. A line typed before then reaches the old process,
	// which echoes it -- marker and all -- and the wait below ends on that
	// echo before the new process has drawn anything. A fast Linux runner gets
	// there first, so the typing waits for the new process.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if sess, _, _ := srv.paneSession(paneID); sess != nil && sess != before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane was never restarted")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The same socket, never reconnected, must reach the new process.
	after := awaitOutput(t, pty, "echo after_restart\r", "after_restart")

	// The process that has gone may have left the emulator on the alternate
	// screen, reporting mouse movement, in application cursor mode -- none of
	// which its replacement knows about or would undo, and all of which the
	// window used to clear for itself on the reconnection a restart cost it.
	// So the reset has to arrive before anything the new process draws.
	//
	// On Windows this pins the ordering rather than the reset itself: ConPTY
	// opens its console with a reset of its own, so the sequence would be
	// there either way.
	reset := strings.Index(after, "\x1bc")
	if reset < 0 {
		t.Fatalf("the terminal was not reset for the new process; saw:\n%q", after)
	}
	if marker := strings.Index(after, "after_restart"); marker < reset {
		t.Error("the reset arrived after the new process had already drawn")
	}
}

// TestRestartedPaneComesBackAtTheMeasuredSize covers what a restart has to
// know: only the browser can measure a pane, and it reports that once, over
// the terminal socket. A restart that does not have it starts the process at
// a conventional 80x24 in a pane that is nothing like that size.
func TestRestartedPaneComesBackAtTheMeasuredSize(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	st := nextState(t, ctl, nil)
	paneID := st.Tabs[0].Root.Pane

	pty := dialPTY(t, srv, paneID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pty.Write(ctx, websocket.MessageText, []byte(`{"resize":{"cols":137,"rows":41}}`)); err != nil {
		t.Fatalf("resize: %v", err)
	}
	nextState(t, ctl, func(s stateMsg) bool {
		p, ok := s.Panes[paneID]
		return ok && p.Cols == 137 && p.Rows == 41
	})

	before, _, _ := srv.paneSession(paneID)
	sendCmd(t, ctl, command{Cmd: "restartPane", ID: paneID})

	// The window never re-reports the size here, exactly as it would not until
	// its next fit, so the new process has to have been started at it.
	var cols, rows int
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if sess, _, _ := srv.paneSession(paneID); sess != nil && sess != before {
			cols, rows = sess.Size()
			if cols == 137 && rows == 41 {
				return
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("restarted pane is %dx%d, want the measured 137x41", cols, rows)
}

// sendResize reports a measured terminal size the way a window's fit does.
func sendResize(t *testing.T, conn *websocket.Conn, cols, rows int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg := fmt.Sprintf(`{"resize":{"cols":%d,"rows":%d}}`, cols, rows)
	if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		t.Fatalf("resize: %v", err)
	}
}

// awaitSize waits for a pane's PTY to settle on a size.
func awaitSize(t *testing.T, srv *Server, paneID string, cols, rows int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var gotC, gotR int
	for time.Now().Before(deadline) {
		if sess, _, _ := srv.paneSession(paneID); sess != nil {
			if gotC, gotR = sess.Size(); gotC == cols && gotR == rows {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pane settled at %dx%d, want %dx%d", gotC, gotR, cols, rows)
}

// sendFocus says a window's terminal has been focused, the way the window does.
func sendFocus(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"focus":true}`)); err != nil {
		t.Fatalf("focus: %v", err)
	}
}

// TestPaneFollowsTheWindowInUse covers two windows on one pane -- the desk, and
// a phone through the relay. The pane used to take the least of every window's
// size, so the phone opened for a glance reflowed the desk's terminal to phone
// width until it was closed. It follows the window being used instead.
func TestPaneFollowsTheWindowInUse(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	desk := dialPTY(t, srv, paneID)
	sendResize(t, desk, 160, 45)
	awaitSize(t, srv, paneID, 160, 45)
	sendFocus(t, desk)

	// The phone reports its size, as every window does on opening, and that
	// is all: the desk is the one in use, and keeps its width.
	phone := dialPTY(t, srv, paneID)
	sendResize(t, phone, 40, 20)
	time.Sleep(300 * time.Millisecond)
	awaitSize(t, srv, paneID, 160, 45)

	// Typing on the phone is using it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := phone.Write(ctx, websocket.MessageBinary, []byte(" ")); err != nil {
		t.Fatalf("type: %v", err)
	}
	awaitSize(t, srv, paneID, 40, 20)

	// Focusing the desk's terminal takes it back.
	sendFocus(t, desk)
	awaitSize(t, srv, paneID, 160, 45)

	// And when the window in use goes, the pane follows the one used before it.
	desk.CloseNow()
	awaitSize(t, srv, paneID, 40, 20)
}

// TestAWindowWithNoSizeTakesNothingOver covers the relay's phone client, which
// reports a size only when asked to fit the pane to its screen. Using it has to
// leave the pane as it is, rather than shrink it to the least of the other
// windows or size it to nothing.
func TestAWindowWithNoSizeTakesNothingOver(t *testing.T) {
	v := &viewerSizes{panes: map[string]map[int64]viewerState{}}
	v.set("pane", 1, 160, 45)
	v.set("pane", 2, 100, 30)
	v.touch("pane", 1)
	if v.touch("pane", 3) {
		t.Error("using a window with no size asked for a refit")
	}
	if cols, rows := v.size("pane"); cols != 160 || rows != 45 {
		t.Errorf("after a window with no size was used the pane is %dx%d, want it left at 160x45", cols, rows)
	}
}

// TestViewerSizesFollowTheWindowInUse covers the bookkeeping behind it, and
// keeps the registry from holding a pane after nobody is watching it, which
// would size the next window that opened against a measurement from one that
// is gone.
func TestViewerSizesFollowTheWindowInUse(t *testing.T) {
	v := &viewerSizes{panes: map[string]map[int64]viewerState{}}

	if cols, rows := v.size("pane"); cols != 0 || rows != 0 {
		t.Errorf("an unwatched pane gave %dx%d, want nothing", cols, rows)
	}

	v.set("pane", 1, 120, 40)
	v.set("pane", 2, 90, 60)
	if cols, rows := v.size("pane"); cols != 90 || rows != 40 {
		t.Errorf("two windows neither of them used gave %dx%d, want the least of each, 90x40", cols, rows)
	}

	if !v.touch("pane", 2) {
		t.Error("using the second window did not move the pane to it")
	}
	if v.touch("pane", 2) {
		t.Error("using the same window again asked for a refit")
	}
	if cols, rows := v.size("pane"); cols != 90 || rows != 60 {
		t.Errorf("with the second window in use, %dx%d, want its 90x60", cols, rows)
	}
	v.touch("pane", 1)
	if cols, rows := v.size("pane"); cols != 120 || rows != 40 {
		t.Errorf("with the first window in use, %dx%d, want its 120x40", cols, rows)
	}

	if !v.drop("pane", 1) {
		t.Error("dropping one of two windows left nothing to resize for")
	}
	if cols, rows := v.size("pane"); cols != 90 || rows != 60 {
		t.Errorf("after the window in use left, %dx%d, want the one used before it, 90x60", cols, rows)
	}

	if v.drop("pane", 2) {
		t.Error("dropping the only window asked for a resize; there is nobody to resize for")
	}
	if len(v.panes) != 0 {
		t.Errorf("registry still holds %d panes after the last window left", len(v.panes))
	}
	if v.drop("pane", 1) {
		t.Error("dropping a window twice asked for a resize")
	}
}

// TestAQuietWindowThatStoppedListeningIsLetGo covers the connection that
// nothing else notices. A terminal socket sits idle whenever the pane is quiet
// or its process has ended, and while it does the server never writes to it,
// so a window that went away without closing would hold the pane's
// subscription and this connection for as long as the instance ran.
func TestAQuietWindowThatStoppedListeningIsLetGo(t *testing.T) {
	defer func(i, o time.Duration) { pingInterval, pingTimeout = i, o }(pingInterval, pingTimeout)
	pingInterval, pingTimeout = 100*time.Millisecond, 300*time.Millisecond

	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	// Dialled and then never read from, so the window never answers a ping --
	// which is what a window whose reader has stopped looks like from here.
	pty := dialPTY(t, srv, paneID)

	time.Sleep(pingInterval + pingTimeout + 500*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, _, err := pty.Read(ctx)
		if err == nil {
			continue
		}
		// Our own deadline expiring is the failure: it means the socket is
		// still there and the server is still holding everything behind it.
		if ctx.Err() != nil {
			t.Fatal("the server is still holding a socket the window stopped answering on")
		}
		return
	}
}

// TestSteadyOutputIsPacedIntoFewerFrames is the case draining a backlog does
// not cover: a pane printing steadily and fast never builds one on a loopback
// socket, because each small write is delivered before the next arrives. Every
// one used to cost a frame, and the window cannot draw more of them than its
// display refreshes.
func TestSteadyOutputIsPacedIntoFewerFrames(t *testing.T) {
	const chunks, size = 400, 64
	// Unbuffered, so the pane and the socket take turns: nothing queues up
	// ahead of the reader, which is what makes this different from a burst.
	out := make(chan []byte)
	conn := streamPair(t, nil, out)

	go func() {
		defer close(out)
		for i := range chunks {
			out <- bytes.Repeat([]byte{byte('a' + i%26)}, size)
		}
	}()

	start := time.Now()
	got, frames := readAllFrames(t, conn)
	elapsed := time.Since(start)

	if len(got) != chunks*size {
		t.Fatalf("got %d bytes, want %d", len(got), chunks*size)
	}
	// Pacing puts a ceiling on frames per second; anything near one frame per
	// chunk means the stream was not paced at all.
	ceiling := int(elapsed/minFrameGap) + 4
	if frames > ceiling {
		t.Errorf("%d chunks over %v arrived in %d frames, want at most %d",
			chunks, elapsed, frames, ceiling)
	}
}

// TestAQuietPaneIsNotPaced is the other half of pacing, and the half that
// matters more: a pane that has not printed for a while must send what it
// prints next straight away. This is the keystroke echo, and holding it for
// even a frame gap would be felt in the typing.
func TestAQuietPaneIsNotPaced(t *testing.T) {
	const echoes = 5
	out := make(chan []byte)
	conn := streamPair(t, nil, out)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var spent time.Duration
	for range echoes {
		time.Sleep(2 * minFrameGap)
		out <- []byte("x")
		start := time.Now()
		if _, _, err := conn.Read(ctx); err != nil {
			t.Fatalf("read: %v", err)
		}
		spent += time.Since(start)
	}
	if spent >= echoes*minFrameGap {
		t.Errorf("%d echoes into a quiet pane took %v in total, which is a frame gap (%v) each",
			echoes, spent, minFrameGap)
	}
}

// dialPTYFrom opens a terminal socket the way a page served from origin would,
// carrying a token as the browser would carry the cookie. host is the address
// the page's own script would reach the server on, which for a real page is
// the one it was loaded from.
func dialPTYFrom(t *testing.T, srv *Server, paneID, origin, host string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", origin)
	return websocket.Dial(ctx,
		"ws://"+host+"/ws/pty?t="+srv.Token()+"&id="+paneID,
		&websocket.DialOptions{HTTPHeader: h})
}

// TestTerminalSocketOnlyAnswersItsOwnPage covers who is allowed to type into
// the agents.
//
// Cookies are shared across ports on a host, so anything else served from
// 127.0.0.1 or localhost -- the user's own dev server, or anything that can be
// made to serve a page from one -- has the token attached to a WebSocket it
// opens here, and WebSocket is not subject to CORS. Only the page this server
// served may open a terminal socket.
func TestTerminalSocketOnlyAnswersItsOwnPage(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	for _, origin := range []string{
		"http://127.0.0.1:9999",
		"http://localhost:9999",
		"https://example.com",
	} {
		conn, resp, err := dialPTYFrom(t, srv, paneID, origin, srv.Addr())
		if err == nil {
			conn.CloseNow()
			t.Errorf("a page served from %s was allowed to open a terminal socket", origin)
			continue
		}
		if resp != nil && resp.StatusCode != http.StatusForbidden {
			t.Errorf("dial from %s = %d, want 403", origin, resp.StatusCode)
		}
	}

	// The window's own page must still be able to, whichever of the loopback
	// names it was opened under: its script builds the socket's address from
	// the one it was loaded from, so the two always agree.
	for _, host := range []string{srv.Addr(), "localhost:" + port(srv.Addr())} {
		conn, _, err := dialPTYFrom(t, srv, paneID, "http://"+host, host)
		if err != nil {
			t.Errorf("the window's own page was refused at %s: %v", host, err)
			continue
		}
		conn.CloseNow()
	}
}

// TestTerminalSocketNeedsTheToken keeps any other local process from typing
// into the agents.
func TestTerminalSocketNeedsTheToken(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws://"+srv.Addr()+"/ws/pty?t=nope&id="+paneID, nil)
	if err == nil {
		conn.CloseNow()
		t.Fatal("a terminal socket was opened without the token")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func port(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

// TestAPaneThatNeverStartedStillTakesASocket covers the first run on a machine
// without Claude Code installed: every agent pane exists and has no process.
// Turning the window away had it redial each of them every few seconds for as
// long as it was open.
func TestAPaneThatNeverStartedStillTakesASocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	// Nothing on PATH, so the `claude` CLI cannot be found and the pane's
	// process never starts.
	t.Setenv("PATH", t.TempDir())

	ws, err := workspace.New(workspace.Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	t.Cleanup(ws.Close)
	if ws.ClaudeAvailable() {
		t.Skip("the claude CLI is still resolvable with an empty PATH")
	}
	ws.NewTab(session.KindClaude, ws.ActiveRoot(), "agent")

	srv, err := New(ws)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	ws.SetWake(srv.Wake)

	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane
	if sess, found, err := srv.paneSession(paneID); err != nil || !found || sess != nil {
		t.Fatalf("expected a pane with no process; got session %v, found %v, err %v", sess, found, err)
	}

	pty := dialPTY(t, srv, paneID)

	// The size the window measured has to be taken even now -- there is no
	// session to apply it to, and it is exactly what the process will be
	// started at once there is one.
	sendResize(t, pty, 111, 37)
	deadline := time.Now().Add(10 * time.Second)
	var cols, rows int
	for time.Now().Before(deadline) {
		if cols, rows = paneSize(t, srv, paneID); cols == 111 && rows == 37 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cols != 111 || rows != 37 {
		t.Errorf("pane remembers %dx%d, want the measured 111x37", cols, rows)
	}

	// It must stay open rather than be hung up on, so the window has no reason
	// to come back and ask again.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, _, err := pty.Read(ctx); ctx.Err() == nil {
		t.Errorf("the socket was closed under the window: %v", err)
	}
}

// paneSize reads the size a pane remembers, on the goroutine that owns it.
func paneSize(t *testing.T, srv *Server, id string) (cols, rows int) {
	t.Helper()
	done := make(chan [2]int, 1)
	srv.do(func() {
		if p := srv.ws.Pane(id); p != nil {
			done <- [2]int{p.Cols, p.Rows}
			return
		}
		done <- [2]int{}
	})
	select {
	case sz := <-done:
		return sz[0], sz[1]
	case <-time.After(5 * time.Second):
		t.Fatal("the workspace did not answer")
		return 0, 0
	}
}

// TestClosingAPaneReleasesItsTerminalSocket covers the end of a pane's life.
// The socket outlives the process in the pane on purpose, so that a restart
// can be picked up on it; a pane that has been closed is not coming back, and
// holding its connection open would hold a goroutine and a poll of the
// workspace for as long as the instance ran.
func TestClosingAPaneReleasesItsTerminalSocket(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	st := nextState(t, ctl, nil)

	// A second pane, so closing the first is an ordinary close rather than the
	// last one going.
	sendCmd(t, ctl, command{Cmd: "splitPane", Dir: "right", Kind: "shell"})
	st = nextState(t, ctl, func(s stateMsg) bool {
		r := s.Tabs[0].Root
		return r != nil && len(r.Children) == 2
	})
	paneID := st.Tabs[0].Root.Children[0].Pane

	pty := dialPTY(t, srv, paneID)
	awaitOutput(t, pty, "echo pane_alive\r", "pane_alive")

	sendCmd(t, ctl, command{Cmd: "closePane", ID: paneID})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for {
		_, _, err := pty.Read(ctx)
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			t.Fatal("the terminal socket outlived the pane it was serving")
		}
		return
	}
}

// TestTypingSurvivesABusyWorkspace covers the moment a new project opens.
// Opening one runs on the workspace goroutine and takes as long as it takes to
// start every agent in it, and it rearranges the window, so every pane already
// on screen reports a new size at the same moment. If reporting a size means
// waiting on that goroutine, nothing anyone types anywhere gets through until
// the project has finished opening.
func TestTypingSurvivesABusyWorkspace(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	pty := dialPTY(t, srv, paneID)
	awaitOutput(t, pty, "echo pane_ready\r", "pane_ready")

	// Occupy the workspace goroutine, as opening a project does, and back up
	// the queue in front of it, as a window's worth of panes all reporting a
	// new size does. That queue is not unbounded, and once it is full anything
	// handing work to the workspace waits its turn.
	release := make(chan struct{})
	var backlog sync.WaitGroup
	defer backlog.Wait()
	defer close(release)
	srv.do(func() { <-release })
	for range cap(srv.cmds) * 2 {
		backlog.Add(1)
		go func() { defer backlog.Done(); srv.do(func() {}) }()
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(srv.cmds) < cap(srv.cmds) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(srv.cmds) < cap(srv.cmds) {
		t.Fatalf("could not fill the workspace queue: %d of %d", len(srv.cmds), cap(srv.cmds))
	}

	sendResize(t, pty, 90, 25)
	awaitOutput(t, pty, "echo typed_through\r", "typed_through")
}

// TestABusyWorkspaceIsNotAGonePane covers the socket held open over an exited
// pane, waiting for someone to restart it. Deciding whether the pane is still
// there means asking the workspace goroutine, and that goroutine can be busy
// for seconds at a time; an unanswered question is not the same as an answer
// of "gone", and treating it as one hangs up on a pane that is still on screen.
func TestABusyWorkspaceIsNotAGonePane(t *testing.T) {
	defer func(d time.Duration) { paneLookup = d }(paneLookup)
	paneLookup = 200 * time.Millisecond

	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	pty := dialPTY(t, srv, paneID)
	awaitOutput(t, pty, "echo pane_ready\r", "pane_ready")

	// End the process in the pane, so its socket is being held for a restart.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := pty.Write(ctx, websocket.MessageBinary, []byte("exit\r")); err != nil {
		t.Fatalf("write exit: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		sess, _, err := srv.paneSession(paneID)
		if err == nil && sess != nil && sess.Exited() {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatal("the process in the pane never ended")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Now make the workspace unable to answer for longer than a look waits.
	release := make(chan struct{})
	var backlog sync.WaitGroup
	defer backlog.Wait()
	defer close(release)
	srv.do(func() { <-release })
	for range cap(srv.cmds) * 2 {
		backlog.Add(1)
		go func() { defer backlog.Done(); srv.do(func() {}) }()
	}
	time.Sleep(4 * paneLookup)

	// The pane is still there, so the socket serving it must be too. Our own
	// deadline expiring is the pass: nothing hung up on us.
	readCtx, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stop()
	for {
		_, _, err := pty.Read(readCtx)
		if err == nil {
			continue
		}
		if readCtx.Err() != nil {
			return
		}
		t.Fatalf("the socket was hung up on while the workspace was busy: %v", err)
	}
}
