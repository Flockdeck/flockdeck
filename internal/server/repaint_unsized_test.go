package server

import (
	"fmt"
	"sync"
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
	// No size is ever sent from here.
	for deadline := time.Now().Add(5 * time.Second); ; {
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
