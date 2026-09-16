package store

import (
	"os"
	"sync"
	"testing"
	"time"
)

// TestWorktreeProcRoundTrip pins what a startup sweep for a stale
// worktree-owning process reads back: the record a pane's process was
// started under, keyed by the pane's own id.
func TestWorktreeProcRoundTrip(t *testing.T) {
	isolateConfig(t)

	if recs, err := LoadWorktreeProcs(); err != nil || len(recs) != 0 {
		t.Fatalf("with nothing recorded, got %v, %v; want empty, nil", recs, err)
	}

	started := time.Now().Round(time.Second)
	if err := TrackWorktreeProc("pane-1", 4321, started, "/repo-wt"); err != nil {
		t.Fatalf("track: %v", err)
	}
	if err := TrackWorktreeProc("pane-2", 4322, started, "/repo-wt2"); err != nil {
		t.Fatalf("track: %v", err)
	}

	recs, err := LoadWorktreeProcs()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("recorded %d processes, want 2: %+v", len(recs), recs)
	}
	got := recs["pane-1"]
	if got.PID != 4321 || got.Path != "/repo-wt" || !got.Started.Equal(started) {
		t.Errorf("pane-1 recorded %+v, want pid 4321, path /repo-wt, started %v", got, started)
	}

	// A pane restarted keeps its id, so recording its new process replaces the
	// record of the old one rather than sitting beside it.
	restarted := started.Add(time.Minute)
	if err := TrackWorktreeProc("pane-1", 4325, restarted, "/repo-wt"); err != nil {
		t.Fatalf("re-track: %v", err)
	}
	recs, err = LoadWorktreeProcs()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("a restart should replace its own record, not add one: %+v", recs)
	}
	if recs["pane-1"].PID != 4325 {
		t.Errorf("pane-1 pid = %d, want 4325 after the restart replaced it", recs["pane-1"].PID)
	}

	// Untracking a pane that closed cleanly removes only its own record.
	if err := UntrackWorktreeProc("pane-1"); err != nil {
		t.Fatalf("untrack: %v", err)
	}
	recs, err = LoadWorktreeProcs()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := recs["pane-1"]; ok {
		t.Error("pane-1 is still on record after being untracked")
	}
	if _, ok := recs["pane-2"]; !ok {
		t.Error("untracking pane-1 removed pane-2's record as well")
	}

	// Untracking the last record removes the file entirely, so a fresh
	// install's config directory does not carry an empty registry forever.
	if err := UntrackWorktreeProc("pane-2"); err != nil {
		t.Fatalf("untrack: %v", err)
	}
	path, err := worktreeProcsPath()
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the registry file was left behind with nothing in it")
	}

	// Untracking an id that was never recorded, or recorded and already gone,
	// is not an error: Close runs it unconditionally.
	if err := UntrackWorktreeProc("never-seen"); err != nil {
		t.Errorf("untrack of an unknown id: %v", err)
	}
}

// TestWorktreeProcsSurviveConcurrentWrites covers a fan-out, which can start
// a dozen panes at once, each recording its own process into the same file.
func TestWorktreeProcsSurviveConcurrentWrites(t *testing.T) {
	isolateConfig(t)

	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			if err := TrackWorktreeProc(id, 1000+i, time.Now(), "/repo-wt"); err != nil {
				t.Errorf("track %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	recs, err := LoadWorktreeProcs()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recs) != n {
		t.Fatalf("recorded %d of %d concurrent panes: %+v", len(recs), n, recs)
	}
}

// TestProcessStartedAtAgreesWithProcessAlive covers the pairing a startup
// sweep leans on to tell a live process from a different one that has since
// been given the same id: a process known to be running answers about when it
// started, or says plainly that it cannot.
func TestProcessStartedAtAgreesWithProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("this process is not reported as alive")
	}
	if _, ok := ProcessStartedAt(0); ok {
		t.Error("process id 0 was reported to have a start time")
	}
}
