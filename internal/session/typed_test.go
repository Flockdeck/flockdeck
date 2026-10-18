package session

import "testing"

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

func TestTypedWritesCountsInputAndNotATerminalsOwnReports(t *testing.T) {
	s := fakeSession(newFakePTY())
	if s.TypedWrites() != 0 {
		t.Fatal("a new session has had input")
	}
	_ = s.WriteString("\x1b[I")
	if s.TypedWrites() != 0 {
		t.Error("a focus report was counted as typing")
	}
	_ = s.WriteString("hello\r")
	if s.TypedWrites() != 1 {
		t.Errorf("TypedWrites = %d, want 1", s.TypedWrites())
	}
}
