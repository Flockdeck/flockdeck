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
	wasAlt, wasResize, wasGap, wasWait := altScreen, repaintResize, repaintGap, unsizedRepaintWait
	t.Cleanup(func() { altScreen, repaintResize, repaintGap, unsizedRepaintWait = wasAlt, wasResize, wasGap, wasWait })
	altScreen = func(*session.Session) bool { return full.Load() }
	// Each window here says its size, and it is that size the repaint is
	// wanted at; one that has not said by the time a slow shell has echoed
	// is TestAWindowThatNeverSaysItsSizeIsRepaintedAnyway's.
	unsizedRepaintWait = time.Hour
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

// TestASecondFreshAttachIsNotRepaintedForWhileTheFirstIsWatching covers a
// second window opening onto a pane that is already open elsewhere -- a phone
// reached through the relay, say, beside the desk's own window, or simply a
// second browser tab. There is one pty behind however many windows are
// attached to a pane, so the repaint that clears a fresh attach's garbled
// replay would drop the desk's own perfectly good screen a row and put it
// back too, for a window it never asked anything for. The second window is
// left with the garbled replay instead, the same as the first would have been
// without this at all -- the ordinary cost of arriving at a pane somebody else
// already has open.
func TestASecondFreshAttachIsNotRepaintedForWhileTheFirstIsWatching(t *testing.T) {
	wasAlt, wasResize, wasGap, wasWait := altScreen, repaintResize, repaintGap, unsizedRepaintWait
	t.Cleanup(func() { altScreen, repaintResize, repaintGap, unsizedRepaintWait = wasAlt, wasResize, wasGap, wasWait })
	altScreen = func(*session.Session) bool { return true }
	unsizedRepaintWait = time.Hour
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
	measure := func(conn *websocket.Conn, cols, rows int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText,
			[]byte(fmt.Sprintf(`{"resize":{"cols":%d,"rows":%d}}`, cols, rows))); err != nil {
			t.Fatalf("report a size: %v", err)
		}
	}

	srv, _ := newTestServer(t)
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane

	// The first window to attach is repainted for, same as ever.
	var h streamHeader
	var held int64
	first := dialResumable(t, srv, paneID, "&from=-1")
	readResumable(t, first, "echo first_view\r", "first_view", &h, &held)
	measure(first, 100, 30)
	for deadline := time.Now().Add(5 * time.Second); ; {
		if got := taken(); len(got) == 2 {
			if got[0] != "100x29" || got[1] != "100x30" {
				t.Fatalf("the repaint went %v; want a row shorter and back, 100x29 then 100x30", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a fresh attach with nobody else watching was not repainted: %v", taken())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A second window, also fresh, attaches while the first is still watching.
	// Its own replay is the same kind of garbled screen the first window's
	// would have been -- but nothing here resizes the pane for it, because the
	// first window is still there to be disrupted by that resize.
	forget()
	var h2 streamHeader
	var held2 int64
	second := dialResumable(t, srv, paneID, "&from=-1")
	readResumable(t, second, "echo second_view\r", "second_view", &h2, &held2)
	if h2.Resumed {
		t.Fatalf("a second window with no resume token of its own was resumed: %+v", h2)
	}
	measure(second, 90, 25)
	time.Sleep(500 * time.Millisecond)
	if got := taken(); len(got) != 0 {
		t.Errorf("a second fresh attach was repainted for while the first was still watching: %v -- "+
			"this would have dropped the first window's own screen a row and put it back, for a "+
			"window it never asked anything for", got)
	}
	first.CloseNow()
	second.CloseNow()
}

// TestTwoSimultaneousFreshAttachesAtLeastOneIsRepainted covers two windows
// attaching to the same freshly-started full-screen program at the same
// instant -- two browser tabs opened together, or a phone reached through
// the relay opening beside the desk's own window in the same moment either
// was first drawn.
//
// Subscribing and deciding whether to repaint used to be two separate looks
// at how many viewers there were, on two different locks (Session.Subscribe
// registering the viewer, then a later Session.Subscribers() call reading
// the count back for armRepaint's decision); two attaches close enough
// together could each register before either asked, and each then find the
// other already there and skip the repaint that clears its own garbled
// replay -- leaving both windows showing garbage until the program's own
// next redraw. Subscribe now hands armRepaint the count taken in the very
// critical section that adds the caller, so whichever of the two is
// serialised first by the session's lock still sees itself alone, and
// repaints, exactly as arriving any less simultaneously would have. This
// goes at Session.Subscribe and Server.armRepaint directly, the way
// TestAWindowThatNeverSaysItsSizeIsRepaintedAnyway does, rather than through
// two real websocket dials: both attaches share the one pty behind the
// pane, and only one goroutine may safely type into it at a time.
func TestTwoSimultaneousFreshAttachesAtLeastOneIsRepainted(t *testing.T) {
	wasAlt, wasResize, wasGap, wasWait := altScreen, repaintResize, repaintGap, unsizedRepaintWait
	t.Cleanup(func() { altScreen, repaintResize, repaintGap, unsizedRepaintWait = wasAlt, wasResize, wasGap, wasWait })
	altScreen = func(*session.Session) bool { return true }
	unsizedRepaintWait = time.Hour
	var mu sync.Mutex
	var steps []string
	repaintResize = func(_ *Server, _ string, cols, rows int) {
		mu.Lock()
		steps = append(steps, fmt.Sprintf("%dx%d", cols, rows))
		mu.Unlock()
	}
	repaintGap = 20 * time.Millisecond

	srv, ws := newTestServer(t)
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane
	sess, ok := ask(srv, func() *session.Session {
		if p := ws.Pane(paneID); p != nil {
			return p.Sess
		}
		return nil
	})
	if !ok || sess == nil {
		t.Fatalf("the pane has no session (answered: %v)", ok)
	}

	// Each of the two windows measures its own size before subscribing, as a
	// real one does on connecting, so armRepaint's already-sized branch fires
	// the repaint at once instead of waiting on unsizedRepaintWait (disabled
	// above, to keep either window's own timer from masking the other's
	// decision).
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			viewer := nextViewer.Add(1)
			viewers.set(paneID, viewer, 100+i, 30)
			<-start
			_, _, _, subscribers := sess.Subscribe()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var repaint atomic.Bool
			srv.armRepaint(ctx, &repaint, paneID, viewer, sess, subscribers, true)
		}(i)
	}
	close(start)
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := ask(srv, func() bool { return true }); !ok {
			t.Fatal("the workspace did not answer")
		}
		mu.Lock()
		n := len(steps)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	got := append([]string(nil), steps...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatalf("neither of two simultaneous fresh attaches was repainted: %v", got)
	}
	if len(got) != 2 {
		t.Errorf("two simultaneous fresh attaches produced %d repaint steps, want exactly 2 (one repaint fired once, not once per window): %v", len(got), got)
	}
}
