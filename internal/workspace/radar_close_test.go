package workspace

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// scratchDirs lists the radar's scratch directories in dir.
func scratchDirs(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "flockdeck-radar-*"))
	return m
}

// useTempDir points the process's temporary folder at a folder of the test's own.
func useTempDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	for _, name := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(name, d)
	}
	return d
}

// Close cancels a radar refresh that is running and waits for it to take down its
// scratch directory, and no refresh after that makes one.
func TestCloseStopsARunningRadarAndRemovesItsScratch(t *testing.T) {
	isolateConfig(t)
	tmp := useTempDir(t)
	r := newRadarRig(t)
	r.setUp()
	r.slow["/repo/a"] = true // a snapshot that waits for the context to end
	w, err := New(Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	fake := radarWorkspace().panes
	w.mu.Lock()
	for id, p := range fake {
		w.panes[id] = p
	}
	w.mu.Unlock()

	done := make(chan struct{})
	go func() {
		w.RefreshGit(func(f func()) { f() })
		close(done)
	}()
	eventually(t, "the radar's scratch directory", func() bool { return len(scratchDirs(tmp)) == 1 })

	// The fake panes have no process to shut down.
	w.mu.Lock()
	for id := range fake {
		delete(w.panes, id)
	}
	w.mu.Unlock()
	began := time.Now()
	w.Close()
	if took := time.Since(began); took > 8*time.Second {
		t.Errorf("Close took %v with a radar refresh running", took)
	}
	select {
	case <-done:
	default:
		t.Error("the refresh was still running when Close returned")
	}
	if left := scratchDirs(tmp); len(left) != 0 {
		t.Errorf("scratch directories left after Close: %v", left)
	}

	// A refresh after Close does not start the radar again.
	before := r.snapshotCount()
	w.mu.Lock()
	for id, p := range fake {
		w.panes[id] = p
	}
	w.mu.Unlock()
	w.RefreshGit(func(f func()) { f() })
	w.mu.Lock()
	for id := range fake {
		delete(w.panes, id)
	}
	w.mu.Unlock()
	if r.snapshotCount() != before || len(scratchDirs(tmp)) != 0 {
		t.Error("a refresh after Close ran the radar")
	}
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Scratch directories a killed run left behind are removed when a workspace is
// made, with the radar off: it may have been on in the run that left them. One
// under an hour old may be another instance's and stays.
func TestStartupSweepsOldScratchDirectoriesWithTheRadarOff(t *testing.T) {
	isolateConfig(t)
	tmp := useTempDir(t)
	old, fresh := filepath.Join(tmp, "flockdeck-radar-old"), filepath.Join(tmp, "flockdeck-radar-fresh")
	for _, d := range []string{old, fresh} {
		if err := os.MkdirAll(filepath.Join(d, "obj"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ago := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, ago, ago); err != nil {
		t.Fatal(err)
	}
	oldOnce, oldSweep, oldEnabled := sweepScratchOnce, radarSweep, radarEnabled
	t.Cleanup(func() { sweepScratchOnce, radarSweep, radarEnabled = oldOnce, oldSweep, oldEnabled })
	sweepScratchOnce = new(sync.Once)
	radarSweep = func() { gitx.SweepScratch(time.Hour) }
	radarEnabled = func() bool { return false }

	w, err := New(Options{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Close)
	eventually(t, "the old scratch directory to be removed", func() bool { _, err := os.Stat(old); return err != nil })
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a scratch directory under an hour old was removed: %v", err)
	}
}
