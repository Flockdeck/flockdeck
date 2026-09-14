package store

import (
	"path/filepath"
	"testing"
)

// TestWorktreeProcessRoundTrips covers recording a pane's process, reading it
// back, and forgetting it again -- the whole lifecycle ReapStaleWorktreeProcesses
// and the pane it was recorded for both depend on.
func TestWorktreeProcessRoundTrips(t *testing.T) {
	isolateConfig(t)

	want := WorktreeProcess{PID: 4242, Started: 99, Cwd: filepath.Join("repo-branch"), Repo: "repo"}
	if err := RecordWorktreeProcess("pane-1", want); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := WorktreeProcesses()
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1: %+v", len(got), got)
	}
	if got[0].PaneID != "pane-1" || got[0].PID != want.PID || got[0].Started != want.Started ||
		got[0].Cwd != want.Cwd || got[0].Repo != want.Repo {
		t.Fatalf("read back %+v, want PaneID pane-1 and %+v", got[0], want)
	}

	if err := ForgetWorktreeProcess("pane-1"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	got, err = WorktreeProcesses()
	if err != nil {
		t.Fatalf("read back after forget: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d records after forgetting the only one, want 0: %+v", len(got), got)
	}
}

// TestForgetWorktreeProcessOfNothingRecorded covers a pane that never worked
// in a worktree, which every ordinary pane close calls this for.
func TestForgetWorktreeProcessOfNothingRecorded(t *testing.T) {
	isolateConfig(t)
	if err := ForgetWorktreeProcess("never-recorded"); err != nil {
		t.Fatalf("forgetting a pane that was never recorded: %v", err)
	}
}

// TestWorktreeProcessesSkipsWhatItCannotRead covers a record left half
// written, or by a version that shaped it differently: one bad file must not
// hide the rest.
func TestWorktreeProcessesSkipsWhatItCannotRead(t *testing.T) {
	isolateConfig(t)
	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	if err := writeAtomic(worktreePath(dir, "bad"), []byte("not json")); err != nil {
		t.Fatalf("write bad record: %v", err)
	}
	if err := RecordWorktreeProcess("good", WorktreeProcess{PID: 1, Cwd: "x", Repo: "y"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := WorktreeProcesses()
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 1 || got[0].PaneID != "good" {
		t.Fatalf("got %+v, want only the good record", got)
	}
}

// TestWorktreeProcessesIgnoresSettingsFiles covers a generated per-session
// settings file sitting in the same directory: SweepSessions goes by age for
// those, and this must not pick them up as worktree records, or age them out
// through the wrong door.
func TestWorktreeProcessesIgnoresSettingsFiles(t *testing.T) {
	isolateConfig(t)
	dir, err := SessionsDir()
	if err != nil {
		t.Fatalf("sessions dir: %v", err)
	}
	if err := writeAtomic(filepath.Join(dir, "pane-2.settings.json"), []byte("{}")); err != nil {
		t.Fatalf("write settings file: %v", err)
	}

	got, err := WorktreeProcesses()
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want a settings file to be ignored", got)
	}
}
