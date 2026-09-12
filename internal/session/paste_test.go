package session

import "testing"

// TestBracketedPasteFollowsTheProgram covers what the prompt bar asks before
// it sends a prompt of several lines: whether the pane's program has bracketed
// paste on now. A program switches it as it goes -- a shell turns it off while
// a command runs and on again at its prompt -- in a sequence that can switch
// other modes too and can arrive split across reads, and a reset takes it away.
func TestBracketedPasteFollowsTheProgram(t *testing.T) {
	s := resumable(64)
	if s.BracketedPaste() {
		t.Fatal("a pane that has printed nothing is said to take pastes")
	}
	for _, step := range []struct {
		out  string
		want bool
	}{
		{"\x1b[?2004h$ ", true},
		{"\x1b[?2004l", false},
		{"\x1b[?1000;2004h", true},
		{"\x1bc", false},
		{"\x1b[?20", false},
		{"04h", true},
		{"\x1b[?1049l", true},
	} {
		s.publish([]byte(step.out))
		if got := s.BracketedPaste(); got != step.want {
			t.Fatalf("after %q, BracketedPaste() = %v, want %v", step.out, got, step.want)
		}
	}
}
