package server

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
)

// TestAWindowThatNeverSaysItsSizeIsRepaintedAnyway covers the phone's window
// onto a full-screen program. The relay's phone client reports a size only
// when asked to fit the pane to its screen, and the repaint that clears a
// fresh attach's garbled replay waited for this window's size -- so the phone
// showed the garbage until the program next redrew of its own accord. Left
// unsized for a moment, the pane is repainted at the size it already is.
func TestAWindowThatNeverSaysItsSizeIsRepaintedAnyway(t *testing.T) {
	wasAlt, wasResize, wasGap, wasWait := altScreen, repaintResize, repaintGap, unsizedRepaintWait
	t.Cleanup(func() { altScreen, repaintResize, repaintGap, unsizedRepaintWait = wasAlt, wasResize, wasGap, wasWait })
	altScreen = func(*session.Session) bool { return true }
	var mu sync.Mutex
	var steps []string
	repaintResize = func(_ *Server, _ string, cols, rows int) {
		mu.Lock()
		steps = append(steps, fmt.Sprintf("%dx%d", cols, rows))
		mu.Unlock()
	}
	repaintGap = 20 * time.Millisecond
	unsizedRepaintWait = 50 * time.Millisecond
	taken := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), steps...)
	}

	srv, ws := newTestServer(t)
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane
	size, ok := ask(srv, func() [2]int {
		p := ws.Pane(paneID)
		if p == nil || p.Sess == nil {
			return [2]int{}
		}
		cols, rows := p.Sess.Size()
		return [2]int{cols, rows}
	})
	if !ok || size[0] <= 0 || size[1] < 2 {
		t.Fatalf("the pane's size is %v (answered: %v)", size, ok)
	}
	shorter, back := fmt.Sprintf("%dx%d", size[0], size[1]-1), fmt.Sprintf("%dx%d", size[0], size[1])

	var h streamHeader
	var held int64
	phone := dialResumable(t, srv, paneID, "&from=-1")
	defer phone.CloseNow()
	readResumable(t, phone, "echo phone_view\r", "phone_view", &h, &held)
	// No size is ever sent from here. The repaint is due 50ms after the attach,
	// but it is made on the workspace goroutine, which a loaded CI runner has
	// been seen to keep busy past five seconds.
	for deadline := time.Now().Add(20 * time.Second); ; {
		if got := taken(); len(got) >= 2 {
			if len(got) != 2 || got[0] != shorter || got[1] != back {
				t.Fatalf("the repaint went %v; want a row shorter and back, %s then %s", got, shorter, back)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a window that never said its size was not repainted: %v", taken())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAWindowThatHasGoneIsNotRepaintedFor covers a window that closes while
// its full-screen pane is still waiting to hear what size it is. The wait
// outlived the connection and repainted the pane anyway, which drops it a row
// and puts it back in every other window watching -- for a window nobody is
// looking at any more.
//
// The wait is ended by the test rather than sat through, so the connection is
// certainly gone by the time it is over.
func TestAWindowThatHasGoneIsNotRepaintedFor(t *testing.T) {
	wasAlt, wasResize, wasGap, wasAfter := altScreen, repaintResize, repaintGap, repaintAfter
	t.Cleanup(func() { altScreen, repaintResize, repaintGap, repaintAfter = wasAlt, wasResize, wasGap, wasAfter })
	altScreen = func(*session.Session) bool { return true }
	var mu sync.Mutex
	var steps int
	repaintResize = func(*Server, string, int, int) {
		mu.Lock()
		steps++
		mu.Unlock()
	}
	// Only a repaint's first step is counted: the one putting the pane back
	// never comes.
	repaintGap = time.Hour
	// armRepaint is called from this goroutine, so waits needs no lock.
	var waits []func()
	repaintAfter = func(_ time.Duration, f func()) *time.Timer {
		waits = append(waits, f)
		return time.AfterFunc(time.Hour, func() {})
	}

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
	// counted is the repaint steps taken so far. The workspace answering
	// first means whatever a finished wait handed it has already run.
	counted := func() int {
		t.Helper()
		if _, ok := ask(srv, func() bool { return true }); !ok {
			t.Fatal("the workspace did not answer")
		}
		mu.Lock()
		defer mu.Unlock()
		return steps
	}

	// A window still connected when its wait is over is repainted for. This is
	// what shows a repaint would be seen here if one were made.
	here, leaveHere := context.WithCancel(context.Background())
	defer leaveHere()
	var hereRepaint atomic.Bool
	srv.armRepaint(here, &hereRepaint, paneID, nextViewer.Add(1), sess, 1, true)
	if len(waits) != 1 {
		t.Fatalf("%d waits for an unsized window's size, want 1", len(waits))
	}
	waits[0]()
	if n := counted(); n != 1 {
		t.Fatalf("a window still there took %d repaint steps, want 1", n)
	}

	gone, leave := context.WithCancel(context.Background())
	var goneRepaint atomic.Bool
	srv.armRepaint(gone, &goneRepaint, paneID, nextViewer.Add(1), sess, 1, true)
	if len(waits) != 2 {
		t.Fatalf("%d waits for an unsized window's size, want 2", len(waits))
	}
	leave()
	waits[1]()
	if n := counted(); n != 1 {
		t.Errorf("a window that had gone was repainted for: %d repaint steps in all, want 1", n)
	}
}
