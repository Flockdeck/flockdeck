package server

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// gatedConn is the server's end of a connection to a window on a slow link.
// Once shut, nothing the server writes goes anywhere until the gate opens, as
// a frame to a phone can take seconds to be on its way.
type gatedConn struct {
	net.Conn
	gate   <-chan struct{}
	shut   *atomic.Bool
	closed chan struct{}
	once   sync.Once
}

func (c *gatedConn) Write(p []byte) (int, error) {
	if c.shut.Load() {
		select {
		case <-c.gate:
		case <-c.closed:
			return 0, net.ErrClosed
		}
	}
	return c.Conn.Write(p)
}

func (c *gatedConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// gatedListener hands out gatedConns.
type gatedListener struct {
	net.Listener
	gate <-chan struct{}
	shut *atomic.Bool
}

func (l gatedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &gatedConn{Conn: c, gate: l.gate, shut: l.shut, closed: make(chan struct{})}, nil
}

// TestAWindowStillTakingItsReplayIsNotPingedAway covers a phone on a slow link
// being sent a pane's history. A frame of it can take most of the write budget
// to arrive, and the keepalive's ping went out behind it and gave up waiting
// for the answer -- so the window was dropped part way through the replay,
// reconnected, was sent the same replay again, and never got past it.
//
// Here the window takes nothing at all until the test lets it, which is many
// times what a ping is given to be answered in.
func TestAWindowStillTakingItsReplayIsNotPingedAway(t *testing.T) {
	const interval, timeout = 10 * time.Millisecond, 10 * time.Millisecond
	replay := bytes.Repeat([]byte("history "), 64<<10)
	gate := make(chan struct{})
	var shut atomic.Bool

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		// The handshake is through; from here on the link is stalled.
		shut.Store(true)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		// Answers to pings arrive through reading, as they do in readInput.
		go func() {
			defer cancel()
			for {
				if _, _, err := conn.Read(ctx); err != nil {
					return
				}
			}
		}()
		var writes writeGauge
		out := make(chan []byte)
		close(out)
		streamed := make(chan bool, 1)
		go func() { streamed <- streamOutput(ctx, conn, replay, out, coalesceLimit, &writes) }()
		// The first frame is under way before anything is pinged, so each
		// ping this test covers is one that would queue behind it.
		for writes.busy.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		go keepalive(ctx, cancel, conn, &writes, interval, timeout)
		if <-streamed {
			_ = conn.Close(websocket.StatusNormalClosure, "stream ended")
		}
	}))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv.Listener = gatedListener{Listener: ln, gate: gate, shut: &shut}
	srv.Start()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 << 20)

	time.Sleep(50 * (interval + timeout))
	close(gate)

	var got []byte
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("the window was dropped %d bytes into a %d-byte replay it was still taking: %v",
					len(got), len(replay), err)
			}
			break
		}
		got = append(got, b...)
	}
	if !bytes.Equal(got, replay) {
		t.Fatalf("got %d bytes, want %d, equal prefix %d", len(got), len(replay), commonPrefix(got, replay))
	}
}

// TestAPingIsAnsweredWithinAWritesBudget covers the other half: a ping that
// does go out behind a frame is given as long for its answer as the frame is
// given to arrive, rather than dropping the window for a frame it was taking.
func TestAPingIsAnsweredWithinAWritesBudget(t *testing.T) {
	if pingTimeout < writeBudget {
		t.Errorf("a ping is given %v to be answered, less than the %v a frame has to arrive", pingTimeout, writeBudget)
	}
}
