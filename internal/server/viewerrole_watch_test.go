package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// A device that only watches is not somebody answering the agent. Its terminal
// must not hold back the push that tells the owner the agent is waiting, as a
// device that can type does.
func TestAWatchersTerminalIsNotUseOfThePane(t *testing.T) {
	srv, ws := newTestServer(t)
	enrolled(srv)
	setAccess(t, srv, "watch-dev", "viewer", true)
	id, since := waitingPane(t, srv, ws)
	ts := remoteServer(t, srv)

	watcher := dialRemotePTYDevice(t, ts, id, "watch-dev")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = watcher.Write(ctx, 1, []byte("x"))
	_ = watcher.Write(ctx, 0, []byte(`{"focus":true}`))
	time.Sleep(300 * time.Millisecond)
	if relayUse.since(id, since) {
		t.Fatal("a watcher was taken for somebody using the pane")
	}
	if n := due(srv, since.Add(30*time.Second)); n == nil {
		t.Error("a watcher on the pane held back the push to the owner")
	}
}

// A device that only watches never has the pane repainted for it: that makes
// the pane a row shorter for a moment, in the desk's own terminal.
func TestAWatcherDoesNotRepaintThePane(t *testing.T) {
	rp := defaultRepaintHooks()
	rp.altScreen = func(*session.Session) bool { return true }
	rp.unsizedWait = 50 * time.Millisecond
	rp.gap = 10 * time.Millisecond
	var mu sync.Mutex
	steps := 0
	rp.resize = func(*Server, string, int, int) {
		mu.Lock()
		steps++
		mu.Unlock()
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return steps
	}

	srv, _ := newTestServer(t)
	srv.rp = rp
	paneID := nextState(t, dialControl(t, srv), nil).Tabs[0].Root.Pane
	setAccess(t, srv, "watch-dev", "viewer", true)
	ts := remoteServer(t, srv)

	watcher := dialRemotePTYDevice(t, ts, paneID, "watch-dev")
	time.Sleep(600 * time.Millisecond)
	if n := count(); n != 0 {
		t.Fatalf("a watcher opening a full-screen pane resized it %d times", n)
	}
	watcher.CloseNow()

	// A device that can type, opening the same kind of pane, is repainted for.
	full := dialRemotePTYDevice(t, ts, paneID, "full-dev")
	defer full.CloseNow()
	waitFor(t, func() bool { return count() >= 2 })
	if got := srv.deviceRole("full-dev"); got != store.RoleFull {
		t.Fatalf("control device is %q", got)
	}
}
