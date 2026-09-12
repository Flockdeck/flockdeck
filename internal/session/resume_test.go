package session

import (
	"strings"
	"testing"
	"time"
)

func resumable(size int) *Session {
	return &Session{history: newRing(size), subs: map[int]*subscriber{}, idleAfter: time.Minute, startedAt: time.Now()}
}

// TestResumeCarriesOnFromTheViewersPlace covers a window whose terminal socket
// dropped and came back. It holds everything up to where it was cut off, so it
// is sent only what came after, and keeps its screen and scrollback.
func TestResumeCarriesOnFromTheViewersPlace(t *testing.T) {
	s := resumable(64)
	s.publish([]byte("first line\n"))

	id, replay, start, resumed, _ := s.SubscribeFrom(s.Epoch(), -1)
	s.Unsubscribe(id)
	if resumed || start != 0 || string(replay) != "first line\n" {
		t.Fatalf("a viewer holding nothing got %q from %d, resumed %v", replay, start, resumed)
	}
	held := start + int64(len(replay))

	s.publish([]byte("second\n"))
	id, replay, start, resumed, _ = s.SubscribeFrom(s.Epoch(), held)
	s.Unsubscribe(id)
	if !resumed || start != held || string(replay) != "second\n" {
		t.Fatalf("resuming from %d got %q from %d, resumed %v; want only what followed", held, replay, start, resumed)
	}

	// Nothing new at all is still a resume, with nothing to send.
	id, replay, _, resumed, _ = s.SubscribeFrom(s.Epoch(), held+int64(len("second\n")))
	s.Unsubscribe(id)
	if !resumed || len(replay) != 0 {
		t.Fatalf("resuming at the end got %q, resumed %v", replay, resumed)
	}
}

// TestResumeCountsWhatIsPutBack covers the modes a fresh replay has to switch
// again because the output that switched them has scrolled away. The window
// counts those bytes like any others, so the place it is told it starts from
// has to allow for them, or its next resume asks for the wrong bytes. A resume
// gets none of them: the window already has the modes, and switching an
// alternate screen on again would clear it.
func TestResumeCountsWhatIsPutBack(t *testing.T) {
	s := resumable(32)
	s.publish([]byte("\x1b[?2004h"))
	s.publish([]byte("enough output to push the switch\nout of the buffer\n"))

	id, replay, start, resumed, _ := s.SubscribeFrom(s.Epoch(), -1)
	s.Unsubscribe(id)
	if resumed || !strings.HasPrefix(string(replay), "\x1b[?2004h") {
		t.Fatalf("a fresh replay %q does not put bracketed paste back first", replay)
	}
	if end := start + int64(len(replay)); end != s.written {
		t.Fatalf("counting the replay from %d ends at %d, not at the output's end %d", start, end, s.written)
	}

	s.publish([]byte("more\n"))
	id, replay, _, resumed, _ = s.SubscribeFrom(s.Epoch(), s.written-int64(len("more\n")))
	s.Unsubscribe(id)
	if !resumed || string(replay) != "more\n" {
		t.Fatalf("a resume was sent %q, want only what followed and nothing put back", replay)
	}
}

// TestResumeStartsAgainWhenItCannot covers the places a window cannot carry on
// from: output that has scrolled out of the buffer, a place past the end, and
// a place in a run of the pane from before it was restarted.
func TestResumeStartsAgainWhenItCannot(t *testing.T) {
	s := resumable(24)
	s.publish([]byte("an early line\n"))
	s.publish([]byte("later lines that push\nthe first out\n"))

	for _, tc := range []struct {
		name         string
		epoch, place int64
	}{
		{"scrolled out", s.Epoch(), 3},
		{"past the end", s.Epoch(), 1 << 20},
		{"another run", s.Epoch() + 1, s.written},
	} {
		id, replay, start, resumed, _ := s.SubscribeFrom(tc.epoch, tc.place)
		s.Unsubscribe(id)
		if resumed {
			t.Errorf("%s: resumed, want a fresh start", tc.name)
		}
		if want := string(s.history.replay()); string(replay) != want {
			t.Errorf("%s: replay %q, want the ordinary one %q", tc.name, replay, want)
		}
		if start != s.written-int64(len(replay)) {
			t.Errorf("%s: start %d does not end the replay at the output's end %d", tc.name, start, s.written)
		}
	}
}
