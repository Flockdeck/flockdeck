package session

import (
	"testing"
	"time"
)

// TestSubscribersCountsWhoIsWatching covers what armRepaint (internal/server)
// checks before resizing a pane to clear a fresh attach's garbled replay: a
// resize is the pane's, not one window's alone, so it must know whether
// anyone besides the window just attaching is already watching.
func TestSubscribersCountsWhoIsWatching(t *testing.T) {
	s := &Session{history: newRing(64), subs: map[int]*subscriber{}, idleAfter: time.Minute}
	if n := s.Subscribers(); n != 0 {
		t.Fatalf("Subscribers() = %d, want 0 before anyone has attached", n)
	}
	id1, _, _ := s.Subscribe()
	if n := s.Subscribers(); n != 1 {
		t.Fatalf("Subscribers() = %d, want 1 with one viewer attached", n)
	}
	id2, _, _ := s.Subscribe()
	if n := s.Subscribers(); n != 2 {
		t.Fatalf("Subscribers() = %d, want 2 with two viewers attached", n)
	}
	s.Unsubscribe(id1)
	if n := s.Subscribers(); n != 1 {
		t.Fatalf("Subscribers() = %d, want 1 after the first viewer left", n)
	}
	s.Unsubscribe(id2)
	if n := s.Subscribers(); n != 0 {
		t.Fatalf("Subscribers() = %d, want 0 once every viewer has gone", n)
	}
}
