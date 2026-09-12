package server

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestAFreshAttachRepaintsAFullScreenProgram covers a window attaching to a
// pane that shows a full-screen program. The replay of one is its screen as it
// happened to be drawn, a piece at a time, so the window showed garbage until
// the program redrew by itself; once the window's size is known the pane is
// made a row shorter and put back, which has the program redraw. A window that
// resumes keeps the screen it had, and a program on the ordinary screen is
// replayed as the output it is, so neither is touched.
func TestAFreshAttachRepaintsAFullScreenProgram(t *testing.T) {
	var full atomic.Bool
	wasAlt, wasResize, wasGap := altScreen, repaintResize, repaintGap
	t.Cleanup(func() { altScreen, repaintResize, repaintGap = wasAlt, wasResize, wasGap })
	altScreen = func(*session.Session) bool { return full.Load() }
	var mu sync.Mutex
	var steps []string
	repaintResize = func(_ *Server, _ string, cols, rows int) {
		mu.Lock()
		steps = append(steps, fmt.Sprintf("%dx%d", cols, rows))
		mu.Unlock()
	}
	repaintGap = 20 * time.Millisecond
	taken := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), steps...)
	}
	forget := func() {
		mu.Lock()
		steps = nil
		mu.Unlock()
	}
	measure := func(conn *websocket.Conn) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"resize":{"cols":100,"rows":30}}`)); err != nil {
			t.Fatalf("report a size: %v", err)
		}
	}
	untouched := func(what string) {
		t.Helper()
		time.Sleep(500 * time.Millisecond)
		if got := taken(); len(got) != 0 {
			t.Errorf("%s was repainted: %v", what, got)
		}
	}

	srv, _ := newTestServer(t)
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane

	// A fresh attach to a full-screen program.
	full.Store(true)
	var h streamHeader
	var held int64
	first := dialResumable(t, srv, paneID, "&from=-1")
	readResumable(t, first, "echo first_view\r", "first_view", &h, &held)
	measure(first)
	for deadline := time.Now().Add(5 * time.Second); ; {
		if got := taken(); len(got) == 2 {
			if got[0] != "100x29" || got[1] != "100x30" {
				t.Fatalf("the repaint went %v; want a row shorter and back, 100x29 then 100x30", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a fresh attach to a full-screen program was not repainted: %v", taken())
		}
		time.Sleep(10 * time.Millisecond)
	}
	first.CloseNow()

	// A resume of the same pane keeps the screen the window already has.
	forget()
	var h2 streamHeader
	var held2 int64
	again := dialResumable(t, srv, paneID, fmt.Sprintf("&from=%d&epoch=%d", held, h.Epoch))
	readResumable(t, again, "echo second_view\r", "second_view", &h2, &held2)
	if !h2.Resumed {
		t.Fatalf("the reconnect was not resumed (%+v), so it cannot show a resume is left alone", h2)
	}
	measure(again)
	untouched("a resumed window")
	again.CloseNow()

	// A fresh attach to a program on the ordinary screen.
	forget()
	full.Store(false)
	plain := dialResumable(t, srv, paneID, "&from=-1")
	readResumable(t, plain, "echo third_view\r", "third_view", &h, &held)
	measure(plain)
	untouched("a program on the ordinary screen")
}
