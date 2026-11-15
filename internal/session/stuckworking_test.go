package session

import (
	"testing"
	"time"
)

// TestAStrayToolAfterTheNudgeSettlesWhenQuiet is the stuck pane of T259: the
// turn ended, the idle nudge came and went, and then a PreToolUse naming no
// subagent landed. It is counted as a stray and shows working, and its
// PostToolUse never comes (the call was refused, or lost). No second nudge is
// sent, so before the fix the pane said working at an empty prompt for good.
func TestAStrayToolAfterTheNudgeSettlesWhenQuiet(t *testing.T) {
	f := newFakePTY()
	t.Cleanup(func() { _ = f.Close() })
	s := fakeSession(f)
	s.Kind = KindClaude
	s.hooksSeen = true
	s.answerQuiet = 100 * time.Millisecond

	for _, ev := range []Event{
		{Name: "UserPromptSubmit"},
		{Name: "Stop"},
		{Name: "Notification", NotificationType: "idle_prompt"},
		{Name: "PreToolUse", Tool: "Bash"},
	} {
		s.ApplyEvent(ev)
	}
	if st, _ := s.Status(); st != StatusWorking {
		t.Fatalf("status = %v right after the stray PreToolUse, want working", st)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st, _ := s.Status(); st == StatusIdle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	st, _ := s.Status()
	t.Fatalf("status = %v after silence, want idle", st)
}

// TestAStrayToolStaysWorkingWhileItDraws is the other side: a tool that is
// still running keeps drawing its clock, and the pane stays working.
func TestAStrayToolStaysWorkingWhileItDraws(t *testing.T) {
	f := newFakePTY()
	t.Cleanup(func() { _ = f.Close() })
	s := fakeSession(f)
	s.Kind = KindClaude
	s.hooksSeen = true
	s.answerQuiet = 250 * time.Millisecond

	s.ApplyEvent(Event{Name: "UserPromptSubmit"})
	s.ApplyEvent(Event{Name: "Stop"})
	s.ApplyEvent(Event{Name: "PreToolUse", Tool: "Bash"})
	for end := time.Now().Add(4 * s.answerQuiet); time.Now().Before(end); {
		s.publish([]byte("⠋ Running… (3s)"))
		time.Sleep(10 * time.Millisecond)
	}
	if st, _ := s.Status(); st != StatusWorking {
		t.Fatalf("status = %v while the tool was drawing, want working", st)
	}
}
