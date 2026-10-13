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
		{"\r", false},
		// An escape sequence ahead of text in one write does not hide the text.
		{"\x1b[Dabc", true},
		{"\r", false},
		{"\x1bOAxyz", true},
		{"\x03", false},
		{"ab\r\x1b[Dcd", true}, // text after the last Enter and a sequence
		{"\x03", false},
		{"\x1b[A\x1b[B", false},
		{"\x1b[", false}, // half a sequence is not text
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

// A slash command can open a menu that no hook reports, and a notice typed
// behind it would pick an option from it.
func TestMenuPossibleFromSubmittedSlashLineUntilAHookSpeaks(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.SetStatus(StatusIdle, "")
	if s.MenuPossible() {
		t.Fatal("a fresh pane may have a menu open")
	}
	for _, in := range []string{"/mod", "el"} {
		_ = s.WriteString(in)
	}
	if s.MenuPossible() {
		t.Error("a slash line not yet submitted counts as a menu")
	}
	_ = s.WriteString("\r")
	if s.DraftPossible() {
		t.Error("the draft flag was not cleared by Enter")
	}
	if !s.MenuPossible() {
		t.Fatal("Enter on /model left the pane deliverable")
	}
	// Re-reporting the same status is still the agent speaking.
	s.SetStatus(StatusIdle, "")
	if s.MenuPossible() {
		t.Error("a hook event after the keystroke did not end the doubt")
	}

	// An ordinary prompt, and a slash that is not first, are no menu.
	_ = s.WriteString("fix a/b\r")
	if s.MenuPossible() {
		t.Error("an ordinary prompt was taken for a slash command")
	}
	// Ctrl+C abandons the line.
	_ = s.WriteString("/mod\x03\r")
	if s.MenuPossible() {
		t.Error("an abandoned slash line was taken for a command")
	}
	// A pasted slash command counts too.
	_ = s.WriteString("\x1b[200~/model\x1b[201~\r")
	if !s.MenuPossible() {
		t.Error("a pasted slash command was missed")
	}
}
