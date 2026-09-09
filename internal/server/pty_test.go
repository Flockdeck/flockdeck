package server

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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

	sendCmd(t, ctl, command{Cmd: "restartPane", ID: paneID})

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

	before, _ := srv.paneSession(paneID)
	sendCmd(t, ctl, command{Cmd: "restartPane", ID: paneID})

	// The window never re-reports the size here, exactly as it would not until
	// its next fit, so the new process has to have been started at it.
	var cols, rows int
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if sess, _ := srv.paneSession(paneID); sess != nil && sess != before {
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
		if sess, _ := srv.paneSession(paneID); sess != nil {
			if gotC, gotR = sess.Size(); gotC == cols && gotR == rows {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pane settled at %dx%d, want %dx%d", gotC, gotR, cols, rows)
}

// TestPaneFitsEveryWindowWatchingIt covers two windows on one pane, which is
// what attaching to a running instance produces. They measure their own
// geometry, so they report different sizes; a pane bigger than the smaller of
// them wraps every line there, and that window has no reason to report again.
func TestPaneFitsEveryWindowWatchingIt(t *testing.T) {
	srv, _ := newTestServer(t)
	ctl := dialControl(t, srv)
	paneID := nextState(t, ctl, nil).Tabs[0].Root.Pane

	first := dialPTY(t, srv, paneID)
	sendResize(t, first, 100, 30)
	awaitSize(t, srv, paneID, 100, 30)

	// The second window is wider but shorter. Letting it win outright would
	// leave the first one wrapping every line, so the pane takes the smaller
	// of each dimension -- which both of them can draw.
	second := dialPTY(t, srv, paneID)
	sendResize(t, second, 200, 20)
	awaitSize(t, srv, paneID, 100, 20)

	// Once the first window has gone the pane is free to widen, and nothing
	// else is going to tell it to: the remaining window's own geometry has not
	// changed, so it has no reason to report again.
	first.CloseNow()
	awaitSize(t, srv, paneID, 200, 20)
}

// TestViewerSizesForgetsTheLastWindow keeps the registry from holding a pane
// after nobody is watching it, which would size the next window that opened
// against a measurement from a window that is gone.
func TestViewerSizesForgetsTheLastWindow(t *testing.T) {
	v := &viewerSizes{panes: map[string]map[int64]termSize{}}

	if cols, rows := v.set("pane", 1, 120, 40); cols != 120 || rows != 40 {
		t.Errorf("one window gave %dx%d, want its own 120x40", cols, rows)
	}
	if _, _, ok := v.drop("pane", 1); ok {
		t.Error("dropping the only window asked for a resize; there is nobody to resize for")
	}
	if len(v.panes) != 0 {
		t.Errorf("registry still holds %d panes after the last window left", len(v.panes))
	}
	if _, _, ok := v.drop("pane", 1); ok {
		t.Error("dropping a window twice asked for a resize")
	}
}
