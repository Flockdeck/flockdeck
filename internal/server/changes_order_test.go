package server

import (
	"encoding/json"
	"testing"
	"time"
)

// TestAReviewListingDoesNotLandOnTopOfANewerOne covers reviewing one checkout
// and then another before the first has been listed. Listing a checkout is
// several git processes, and a large one takes a while: its listing landed on
// top of the second's, the panel drew whichever came last, and its Commit then
// committed in the checkout nobody was looking at.
func TestAReviewListingDoesNotLandOnTopOfANewerOne(t *testing.T) {
	srv, _ := newTestServer(t)
	slow, quick := t.TempDir(), t.TempDir()
	held, release := make(chan struct{}), make(chan struct{})
	was := readChanges
	t.Cleanup(func() { readChanges = was })
	readChanges = func(dir string) changesMsg {
		if dir == slow {
			close(held)
			<-release
		}
		return changesMsg{Type: "changes", Cwd: dir}
	}
	listed := func(c *controlClient, within time.Duration) (string, bool) {
		t.Helper()
		select {
		case raw := <-c.out:
			var msg changesMsg
			if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != "changes" {
				t.Fatalf("got %s; want a listing", raw)
			}
			return msg.Cwd, true
		case <-time.After(within):
			return "", false
		}
	}

	c := &controlClient{out: make(chan []byte, 8)}
	srv.listChanges(c, slow, false)
	<-held
	srv.listChanges(c, quick, false)
	if got, ok := listed(c, 5*time.Second); !ok || got != quick {
		t.Fatalf("the second checkout's listing was %q (arrived: %v); want %q", got, ok, quick)
	}
	close(release)
	if got, ok := listed(c, 500*time.Millisecond); ok {
		t.Fatalf("the first checkout's listing (%q) landed after the second's", got)
	}
}

// TestAListingThatPanicsDoesNotKeepItsWindow covers a listing that panics. The
// panic is recovered, but the window's entry in the table of newest requests
// was never removed, so the window stayed reachable from it for good.
func TestAListingThatPanicsDoesNotKeepItsWindow(t *testing.T) {
	srv, _ := newTestServer(t)
	was := readChanges
	t.Cleanup(func() { readChanges = was })
	panicked := make(chan struct{})
	readChanges = func(dir string) changesMsg {
		defer close(panicked)
		panic("an odd repository state")
	}
	c := &controlClient{out: make(chan []byte, 8)}
	srv.listChanges(c, t.TempDir(), false)
	<-panicked

	for deadline := time.Now().Add(5 * time.Second); ; {
		changeListings.Lock()
		_, held := changeListings.seq[c]
		changeListings.Unlock()
		if !held {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the window is still in the table of newest listings after its listing panicked")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
