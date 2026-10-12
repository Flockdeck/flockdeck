package session

import "testing"

// A prompt may hold text the user has typed and not sent, and Flockdeck must not
// type a notice onto the end of it.
func TestDraftPossibleFollowsWhatWasTypedSinceTheLastEnter(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.status = StatusIdle
	if s.DraftPossible() {
		t.Fatal("a pane nobody typed into has a draft")
	}
	for _, step := range []struct {
		input string
		want  bool
	}{
		{"fix th", true},
		{"e bug", true},
		{"\r", false},
		{"\x1b[A", false}, // arrow keys add no text
		{"\x1b", false},   // Escape alone adds none
		{"\x7f", false},   // nor does Backspace on an empty prompt
		{"a", true},       // a key starts one
		{"\x7f", true},    // Backspace does not prove it is empty
		{"\x15", false},   // Ctrl+U clears the line
		{"\x1b[200~long paste\x1b[201~", true},
		{"\x03", false}, // Ctrl+C
		{"draft\r", false},
		{"one\rtwo", true}, // text after the last Enter is a draft
	} {
		if err := s.WriteString(step.input); err != nil {
			t.Fatal(err)
		}
		if got := s.DraftPossible(); got != step.want {
			t.Fatalf("after %q DraftPossible = %v, want %v", step.input, got, step.want)
		}
	}
}

// A terminal's own reports are not typing.
func TestDraftIgnoresTerminalReports(t *testing.T) {
	s := fakeSession(newFakePTY())
	if err := s.WriteString("\x1b[I"); err != nil { // focus gained
		t.Fatal(err)
	}
	if s.DraftPossible() {
		t.Error("a focus report was taken for typing")
	}
}

func TestClosingAndHooksReportedAreFalseUntilTheyHappen(t *testing.T) {
	s := fakeSession(newFakePTY())
	if s.Closing() || s.HooksReported() {
		t.Fatal("a new session is closing or has heard from hooks")
	}
	s.SetStatus(StatusIdle, "")
	if !s.HooksReported() {
		t.Error("a status from a hook did not count as hooks reporting")
	}
	_ = s.Close()
	if !s.Closing() {
		t.Error("Close did not mark the session as closing")
	}
}
