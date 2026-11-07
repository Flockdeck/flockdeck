package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWorktreeProcsWaitForAnotherInstancesFileLock covers two -solo instances
// sharing a state folder. Each read the whole file, changed one record and wrote
// it back, so the one that wrote last undid the other's change. Here another
// holder has the file lock, writes its own record while it holds it, and lets go:
// the record being tracked must come out beside it, not instead of it.
func TestWorktreeProcsWaitForAnotherInstancesFileLock(t *testing.T) {
	isolateConfig(t)
	path, err := worktreeProcsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	other, err := lockFile(path + ".lock")
	if err != nil {
		t.Fatalf("take the lock as the other instance: %v", err)
	}
	if again, err := lockFile(path + ".lock"); err == nil {
		again()
		other()
		t.Skip("this platform has no file lock to take")
	}

	done := make(chan error, 1)
	go func() { done <- TrackWorktreeProc("mine", 1, time.Now(), "/mine") }()
	select {
	case err := <-done:
		t.Fatalf("Track returned (%v) while another instance held the lock", err)
	case <-time.After(200 * time.Millisecond):
	}

	// The other instance writes its record, as it would between its read and write.
	data, _ := json.Marshal(map[string]WorktreeProc{"theirs": {PID: 2, Path: "/theirs"}})
	if err := writeAtomic(path, data); err != nil {
		t.Fatal(err)
	}
	other()
	if err := <-done; err != nil {
		t.Fatalf("track: %v", err)
	}
	recs, err := LoadWorktreeProcs()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := recs["theirs"]; !ok || recs["mine"].PID != 1 {
		t.Errorf("records are %+v, want both mine and theirs", recs)
	}
}

// A lock that never comes free does not stop a pane starting.
func TestWorktreeProcsGoOnWhenTheFileLockStaysHeld(t *testing.T) {
	isolateConfig(t)
	old := worktreeProcsLockWait
	worktreeProcsLockWait = 50 * time.Millisecond
	t.Cleanup(func() { worktreeProcsLockWait = old })
	path, err := worktreeProcsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	other, err := lockFile(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer other()
	if err := TrackWorktreeProc("mine", 1, time.Now(), "/mine"); err != nil {
		t.Fatalf("track with the lock held: %v", err)
	}
}
