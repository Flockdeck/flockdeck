package session

import (
	"testing"
	"time"
)

// TestKillProcessTreeEndsWhatWasLeftRunning covers what
// ReapStaleWorktreeProcesses exists for: a pane's process that a past run
// never got the chance to close, found again only by its bare process id and
// the Started reading taken when it was recorded -- not by anything tying it
// to this run, since nothing does.
func TestKillProcessTreeEndsWhatWasLeftRunning(t *testing.T) {
	s, child := startTree(t, "")
	pid := s.Pid()
	started, ok := Started(pid)
	if !ok {
		t.Fatal("Started could not read a process this test just started")
	}

	if !KillProcessTree(pid, started) {
		t.Fatal("KillProcessTree reported nothing was running")
	}

	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) || alive(child) {
		if !time.Now().Before(deadline) {
			t.Fatalf("KillProcessTree left something running: pane alive=%v, child alive=%v", alive(pid), alive(child))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestKillProcessTreeLeavesAMismatchedPIDAlone covers a process id that has
// been handed to an unrelated process since it was recorded: a Started
// reading that no longer matches must stop it from being ended by mistake.
func TestKillProcessTreeLeavesAMismatchedPIDAlone(t *testing.T) {
	s, _ := startTree(t, "")
	pid := s.Pid()
	started, ok := Started(pid)
	if !ok {
		t.Fatal("Started could not read a process this test just started")
	}

	if KillProcessTree(pid, started+1) {
		t.Fatal("KillProcessTree ended a process whose Started reading did not match the hint")
	}
	if !alive(pid) {
		t.Fatal("a process was ended despite its Started reading not matching")
	}
}

// TestKillProcessTreeOfNothingRunning covers the common case: a pane whose
// process had already exited on its own, closed the ordinary way or simply
// finished, by the time a later run went looking for it. Reporting nothing
// running is the right answer, not a failure.
func TestKillProcessTreeOfNothingRunning(t *testing.T) {
	s, _ := startTree(t, "")
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if KillProcessTree(s.Pid(), 0) {
		t.Fatal("KillProcessTree reported a process running that had already been closed")
	}
}

// TestStartedIsStableForARunningProcess covers the assumption
// ReapStaleWorktreeProcesses relies on: reading a running process twice gives
// the same answer, so recording it once and comparing later actually works.
func TestStartedIsStableForARunningProcess(t *testing.T) {
	s, _ := startTree(t, "")
	pid := s.Pid()
	a, ok := Started(pid)
	if !ok {
		t.Fatal("Started could not read a process this test just started")
	}
	b, ok := Started(pid)
	if !ok {
		t.Fatal("Started could not read the same process a second time")
	}
	if a != b {
		t.Fatalf("Started read %d and then %d for the same running process", a, b)
	}
}
