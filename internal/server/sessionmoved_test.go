package server

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// A pane whose agent moved its session id calls the workspace's hook from its
// runtime goroutine (main wires it to SessionMoved). The layout is written at
// once, not at the next interval, and the write is queued onto the workspace
// goroutine: the caller does not touch the workspace's tabs, which belong to
// that goroutine. The id itself is read into the layout by the workspace's own
// test; this one covers the chain from the hook to the file on disk.
func TestAMovedSessionSavesTheLayoutAtOnceOnTheWorkspaceGoroutine(t *testing.T) {
	srv, ws := newTestServer(t)
	root := ws.ActiveRoot()

	// The layout on disk has only the first tab, and the timed save is thirty
	// seconds away, so a save that follows is the hook's.
	if err, ok := ask(srv, func() error { return ws.SaveProject(root) }); !ok || err != nil {
		t.Fatalf("save: %v", err)
	}
	ask(srv, func() bool { ws.NewTab(session.KindShell, root, "after-move"); return true })

	// The workspace goroutine keeps changing the tabs while hooks arrive from
	// others, so a save run on the caller's goroutine is a data race.
	stop := make(chan struct{})
	var churned atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			srv.do(func() {
				ws.NewTab(session.KindShell, root, fmt.Sprintf("churn-%d", i))
				churned.Add(1)
			})
			time.Sleep(time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		// Until the tabs have changed several times while hooks were arriving.
		giveUp := time.Now().Add(20 * time.Second)
		for i := 0; (i < 20 || churned.Load() < 5) && time.Now().Before(giveUp); i++ {
			srv.SessionMoved("pane")
			time.Sleep(time.Millisecond)
		}
	}()

	deadline := time.Now().Add(15 * time.Second)
	for {
		st, err := store.Load(root)
		if err == nil && st != nil {
			found := false
			for _, tab := range st.Tabs {
				found = found || tab.Title == "after-move"
			}
			if found {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the layout was not saved after the session moved: %+v, %v", st, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
}
