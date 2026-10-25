package session

import (
	"testing"
	"time"
)

// The agent's own idle/busy word replaces a hook status that is older than it and
// disagrees, in both directions, and never one the hooks said after it.
func TestReconcileFollowsTheAgentWhereHooksAreStale(t *testing.T) {
	const settle = 3 * time.Second
	long := time.Now().Add(-time.Minute)

	s := claudePane()
	s.SetStatus(StatusIdle, "")
	s.statusSince = long.Add(-time.Hour)
	// Idle by the hooks, busy by the agent's file, which changed a minute ago.
	if !s.Reconcile(true, long, settle) {
		t.Fatal("a busy agent shown idle was not corrected")
	}
	if st, _ := s.Status(); st != StatusWorking {
		t.Errorf("status = %v, want working", st)
	}

	s.statusSince = long.Add(-time.Hour)
	// Working by the hooks, idle by the file.
	if !s.Reconcile(false, long, settle) {
		t.Fatal("an idle agent shown working was not corrected")
	}
	if st, _ := s.Status(); st != StatusIdle {
		t.Errorf("status = %v, want idle", st)
	}

	// Agreement changes nothing.
	if s.Reconcile(false, long, settle) {
		t.Error("a status that agrees was changed")
	}
}

func TestReconcileLeavesTheHooksTheirNewerWordAndTheStatusesItCannotJudge(t *testing.T) {
	const settle = 3 * time.Second
	s := claudePane()
	s.SetStatus(StatusWorking, "Bash") // a hook just now
	// The file's change is older than the hook's status: the hook wins.
	if s.Reconcile(false, time.Now().Add(-time.Minute), settle) {
		t.Error("a file older than the hooks' status overruled it")
	}
	// A change that has not stood for the settle time is not acted on.
	s.statusSince = time.Now().Add(-time.Hour)
	if s.Reconcile(false, time.Now().Add(-time.Second), settle) {
		t.Error("a change inside the settle time was acted on")
	}
	// Waiting and blocked are not told apart from busy and idle by the file.
	for _, st := range []Status{StatusWaiting, StatusBlocked} {
		s.SetStatus(st, "x")
		s.statusSince = time.Now().Add(-time.Hour)
		for _, busy := range []bool{true, false} {
			if s.Reconcile(busy, time.Now().Add(-time.Minute), settle) {
				t.Errorf("a %v pane was changed by an agent saying busy=%v", st, busy)
			}
		}
	}
}
