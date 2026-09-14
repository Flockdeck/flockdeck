package session

import (
	"testing"
	"time"
)

// TestKillTreeEndsWhatWasLeftRunning covers what a startup sweep for a stale
// worktree-owning process exists for: a pane's process a past run never got
// the chance to close, found again only by its bare process id -- not by
// anything tying it to this run, since nothing does -- and ended along with
// everything it started, the same way closing its own pane would have.
func TestKillTreeEndsWhatWasLeftRunning(t *testing.T) {
	s, child := startTree(t, "")
	pid := s.Pid()

	KillTree(pid)

	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) || alive(child) {
		if !time.Now().Before(deadline) {
			t.Fatalf("KillTree left something running: pane alive=%v, child alive=%v", alive(pid), alive(child))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestKillTreeOfNothingRunning covers the common case: a pane whose process
// had already exited on its own, closed the ordinary way or simply finished,
// by the time a later run went looking for it. KillTree is not asked to tell
// the difference -- a caller only calls it once store.ProcessAlive has
// already confirmed something is there -- but it must not panic or hang over
// a pid with nothing left to end.
func TestKillTreeOfNothingRunning(t *testing.T) {
	s, _ := startTree(t, "")
	pid := s.Pid()
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	KillTree(pid)
}
