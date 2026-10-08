package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Closing a pane that was touched from the relay leaves nothing behind in
// relayUse: mark's only deletion path used to be since, called only for
// panes still open when a relay push iterates them, so a pane closed after
// being opened from a phone had its entry sit in relayUse.at for the life of
// the process. See Workspace.SetPaneClosedHook and (*Server).PaneClosed.
func TestClosingAPaneForgetsRelayUse(t *testing.T) {
	srv, ws := newTestServer(t)
	id := addPane(t, srv, ws, "second")

	ts := remoteServer(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", ts.URL)
	h.Set("Flockdeck-Remote-Device", "dev-1")
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+id, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("open the pane through the relay: %v", err)
	}
	waitFor(t, func() bool { return relayUse.has(id) })
	conn.Close(websocket.StatusNormalClosure, "")

	if ok, _ := ask(srv, func() bool { return ws.ClosePaneByID(id) }); !ok {
		t.Fatal("closing the pane was refused")
	}

	waitFor(t, func() bool { return !relayUse.has(id) })
}

// TestAPaneClosedUnderItsRelaySocketLeavesNoRelayUse covers the other order:
// the pane is closed while the phone's socket to it is still open. The socket
// ends after the pane has gone, and what it did on the way out was to record
// the pane as used, which put back the entry PaneClosed had just removed and
// left it for the life of the process.
func TestAPaneClosedUnderItsRelaySocketLeavesNoRelayUse(t *testing.T) {
	srv, ws := newTestServer(t)
	ts := remoteServer(t, srv)
	for round := range 10 {
		id := addPane(t, srv, ws, fmt.Sprintf("second%d", round))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		h := http.Header{}
		h.Set("Origin", ts.URL)
		h.Set("Flockdeck-Remote-Device", "dev-1")
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+id, &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			t.Fatalf("open the pane through the relay: %v", err)
		}
		waitFor(t, func() bool { return relayUse.has(id) })

		if ok, _ := ask(srv, func() bool { return ws.ClosePaneByID(id) }); !ok {
			t.Fatal("closing the pane was refused")
		}
		// The socket ends on its own once its pane is gone.
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				break
			}
		}
		cancel()
		conn.CloseNow()
		// The server's side of the socket has its own way out after the
		// client has gone, and the entry is looked at once it has been
		// through the workspace goroutine, which it goes through on the way.
		if _, ok := ask(srv, func() bool { return true }); !ok {
			t.Fatal("the workspace did not answer")
		}
		time.Sleep(50 * time.Millisecond)
		if relayUse.has(id) {
			t.Fatalf("round %d: the closed pane %s was left in relayUse", round, id)
		}
	}
}

// TestRelayUseNotedAfterAPaneClosedIsNotRecorded covers a keystroke or a focus
// frame from a phone arriving after the pane was closed and before its socket
// ended. They were recorded on the socket's own goroutine, so the entry
// PaneClosed had removed came back and stayed for the life of the process.
func TestRelayUseNotedAfterAPaneClosedIsNotRecorded(t *testing.T) {
	srv, ws := newTestServer(t)
	id := addPane(t, srv, ws, "second")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	markUse := srv.relayMarker(ctx, id)

	markUse()
	waitFor(t, func() bool { return relayUse.has(id) })

	if ok, _ := ask(srv, func() bool { return ws.ClosePaneByID(id) }); !ok {
		t.Fatal("closing the pane was refused")
	}
	for range 20 {
		markUse()
		time.Sleep(5 * time.Millisecond)
	}
	// What was noted has been through the workspace goroutine by now.
	if _, ok := ask(srv, func() bool { return true }); !ok {
		t.Fatal("the workspace did not answer")
	}
	time.Sleep(50 * time.Millisecond)
	if relayUse.has(id) {
		t.Fatalf("a use noted after the pane %s closed was recorded", id)
	}
}

// The same through a socket: typed input and focus frames sent while the pane
// is closed leave no record, ten panes over.
func TestTypedInputAndFocusAfterAPaneClosedLeaveNoRelayUse(t *testing.T) {
	srv, ws := newTestServer(t)
	ts := remoteServer(t, srv)
	for round := range 10 {
		id := addPane(t, srv, ws, fmt.Sprintf("pane%d", round))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		h := http.Header{}
		h.Set("Origin", ts.URL)
		h.Set("Flockdeck-Remote-Device", "dev-1")
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+id, &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			t.Fatalf("open the pane through the relay: %v", err)
		}
		waitFor(t, func() bool { return relayUse.has(id) })

		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if conn.Write(ctx, websocket.MessageBinary, []byte("x")) != nil ||
					conn.Write(ctx, websocket.MessageText, []byte(`{"focus":true}`)) != nil {
					return
				}
			}
		}()
		time.Sleep(20 * time.Millisecond)
		if ok, _ := ask(srv, func() bool { return ws.ClosePaneByID(id) }); !ok {
			t.Fatal("closing the pane was refused")
		}
		time.Sleep(50 * time.Millisecond)
		close(stop)
		wg.Wait()
		cancel()
		conn.CloseNow()
		if _, ok := ask(srv, func() bool { return true }); !ok {
			t.Fatal("the workspace did not answer")
		}
		time.Sleep(50 * time.Millisecond)
		if relayUse.has(id) {
			t.Fatalf("round %d: input after the pane %s closed left it in relayUse", round, id)
		}
	}
}

// TestOpeningARelaySocketDoesNotWaitOnTheWorkspace covers a phone opening a
// pane's terminal while the workspace goroutine is busy. Finding the pane is
// the one question it has to ask; recording the open as a use waited for the
// goroutine a second time, up to paneLookup, before the terminal was served.
// The workspace is stalled the moment the pane has been found, and the first
// frame has to arrive well inside the wait the open used to have.
func TestOpeningARelaySocketDoesNotWaitOnTheWorkspace(t *testing.T) {
	srv, ws := newTestServer(t)
	id := addPane(t, srv, ws, "second")
	srv.paneLookup = 3 * time.Second

	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free)
	srv.paneFound = func() {
		// Queued, not waited for: the workspace goroutine is held from here.
		srv.cmds <- func() { <-release }
	}
	ts := remoteServer(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", ts.URL)
	h.Set("Flockdeck-Remote-Device", "dev-1")
	began := time.Now()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/pty?id="+id, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("open the pane through the relay: %v", err)
	}
	defer conn.CloseNow()
	readCtx, stop := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer stop()
	if _, _, err := conn.Read(readCtx); err != nil {
		t.Fatalf("no frame within %v of opening with the workspace stalled (opened in %v): %v",
			1500*time.Millisecond, time.Since(began), err)
	}
	free()
	// The open is still recorded, once the workspace is free.
	waitFor(t, func() bool { return relayUse.has(id) })
}
