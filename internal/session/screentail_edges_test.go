package session

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFlockdeckInputIsKeptApartFromTheUsers(t *testing.T) {
	s := fakeSession(newFakePTY())
	if err := s.WriteStringFrom(InputFlockdeck, "Flockdeck: new mail"); err != nil {
		t.Fatal(err)
	}
	if !s.LastUserInput().IsZero() {
		t.Error("Flockdeck's own line was recorded as the user typing")
	}
	if s.LastFlockdeckInput().IsZero() {
		t.Error("Flockdeck's own line was not recorded")
	}
	_ = s.WriteString("x")
	if s.LastUserInput().IsZero() {
		t.Error("the user's keystroke was not recorded")
	}
	// Both still count as typing for everything that already read it.
	if s.TypedWrites() != 2 {
		t.Errorf("TypedWrites = %d, want 2", s.TypedWrites())
	}
}

func TestKittyFlagsReportIsATerminalReportButAKeyIsNot(t *testing.T) {
	s := fakeSession(newFakePTY())
	_, _ = s.Write([]byte("\x1b[?0u"))
	if !s.LastUserInput().IsZero() {
		t.Error("the keyboard flags reply was counted as typing")
	}
	_, _ = s.Write([]byte("\x1b[97u")) // the key a, in the kitty encoding
	if s.LastUserInput().IsZero() {
		t.Error("a kitty-encoded key was not counted as typing")
	}
}

func TestScreenTailHoldsBackACharacterCutByAChunk(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.publish([]byte("caf\xc3"))
	first := s.ScreenTail()
	if got := strings.Join(first.Lines, "|"); got != "caf" {
		t.Fatalf("tail = %q, want the torn character held back", got)
	}
	for _, l := range first.Lines {
		if !utf8.ValidString(l) {
			t.Fatalf("invalid UTF-8 in %q", l)
		}
	}
	time.Sleep(15 * time.Millisecond)
	s.publish([]byte("\xa9 ready"))
	second := s.ScreenTail()
	if got := strings.Join(second.Lines, "|"); got != "café ready" {
		t.Fatalf("tail = %q after the rest arrived", got)
	}
	if !second.ChangedAt.After(first.ChangedAt) {
		t.Error("completing the text did not move ChangedAt")
	}
}

func TestScreenTailTornCharacterDoesNotMoveChangedAt(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.publish([]byte("done\ncaf"))
	first := s.ScreenTail().ChangedAt
	time.Sleep(15 * time.Millisecond)
	s.publish([]byte("\xc3"))
	if got := s.ScreenTail().ChangedAt; !got.Equal(first) {
		t.Errorf("a half character moved ChangedAt from %v to %v", first, got)
	}
}

func TestScreenTailOfOutputWithNoLineBreakStartsOnACharacter(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.history = newRing(20000)
	s.publish([]byte(strings.Repeat("€", 10000))) // 30000 bytes of 3-byte characters
	tail := s.ScreenTail()
	if len(tail.Lines) != 1 || !utf8.ValidString(tail.Lines[0]) || tail.Lines[0] == "" {
		t.Fatalf("tail is not valid text: %d lines", len(tail.Lines))
	}
	if strings.Trim(tail.Lines[0], "€") != "" {
		t.Errorf("tail holds something other than the character")
	}
}

func TestScreenTailTrackingLapsesWhenNobodyAsks(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.publish([]byte("one\n"))
	_ = s.ScreenTail()
	s.mu.Lock()
	s.tailAsked = time.Now().Add(-2 * tailIdleAfter)
	s.mu.Unlock()
	s.publish([]byte("two\n"))

	s.mu.RLock()
	watched, text := s.tailWatched, s.tailText
	s.mu.RUnlock()
	if watched || text != "one" {
		t.Fatalf("watched = %v, text = %q: tracking should have stopped without refreshing", watched, text)
	}
	got := s.ScreenTail()
	if strings.Join(got.Lines, "|") != "one|two" {
		t.Errorf("tail after the lapse = %q, want it caught up", got.Lines)
	}
}
