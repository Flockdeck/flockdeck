package session

import (
	"testing"
	"time"
)

// TestAltScreenFollowsTheProgram covers what the terminal socket asks before a
// window starting afresh on a pane is given its replay: whether the program is
// full-screen, in which case the replay is its screen drawn a piece at a time
// and it has to be asked to redraw.
func TestAltScreenFollowsTheProgram(t *testing.T) {
	s := &Session{history: newRing(64), subs: map[int]*subscriber{}, idleAfter: time.Minute}
	if s.AltScreen() {
		t.Fatal("a pane that has said nothing is on the alternate screen")
	}
	// Split across reads, the way a PTY delivers it.
	s.publish([]byte("\x1b[?10"))
	s.publish([]byte("49h"))
	if !s.AltScreen() {
		t.Error("switching to the alternate screen (1049) was not seen")
	}
	s.publish([]byte("\x1b[?1049l"))
	if s.AltScreen() {
		t.Error("switching back from the alternate screen was not seen")
	}
	s.publish([]byte("\x1b[?47h"))
	if !s.AltScreen() {
		t.Error("the older alternate screen (47) was not seen")
	}
	s.publish([]byte("\x1bc"))
	if s.AltScreen() {
		t.Error("a full reset left the pane on the alternate screen")
	}
}
