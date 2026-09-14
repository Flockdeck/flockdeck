package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
	"github.com/jmwri/flockdeck/internal/layout"
	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// waitProcessGone polls until pid is no longer running, or fails the test.
// Ending a process is not always visible to a caller the instant a reaper
// call returns -- the OS can take a moment to finish tearing it down after
// the handle it was waited on has already signalled -- so this is what every
// test that reaps a real process checks with, rather than a single immediate
// read.
func waitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for store.ProcessAlive(pid) {
		if !time.Now().Before(deadline) {
			t.Fatalf("process %d was still running five seconds after being reaped", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// setupWorktreePane opens a project at repo and splits a shell pane into
// worktree, a sibling directory standing in for a git worktree cut from it --
// the same layout a fan-out leaves (see gitx.WorktreePaths). Neither
// directory needs to be a real git repository for most of what the reaper
// does; TestReapStaleWorktreeProcessesLeavesARegisteredWorktreeAlone is the
// one test that needs a real one, and builds its own before calling this.
func setupWorktreePane(t *testing.T, home, repo, worktree string) (ws *Workspace, plain, wt *Pane) {
	t.Helper()
	for _, dir := range []string{repo, worktree} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ws = newTestWorkspace(t, home)
	if err := ws.OpenProject(repo); err != nil {
		t.Fatalf("open %s: %v", repo, err)
	}
	ws.NewTab(session.KindShell, repo, "lead")
	plain = ws.FocusedPane()
	if plain == nil || plain.Sess == nil {
		t.Fatal("the plain pane did not start")
	}

	ws.SplitPaneInWith(layout.Horizontal, Choice{Kind: session.KindShell}, worktree)
	wt = ws.FocusedPane()
	if wt == nil || wt.Sess == nil {
		t.Fatal("the worktree pane did not start")
	}
	return ws, plain, wt
}

// TestWorktreePaneIsRecordedAndForgotten covers the whole point of recording
// a worktree pane's process: the record appears the moment the pane starts,
// names only a pane genuinely working outside its own project, and is gone
// again the moment the pane is closed the ordinary way.
func TestWorktreePaneIsRecordedAndForgotten(t *testing.T) {
	isolateConfig(t)
	home := t.TempDir()
	repo := filepath.Join(home, "code", "repo")
	worktree := filepath.Join(home, "code", "repo-agent-fix")
	ws, plain, wt := setupWorktreePane(t, home, repo, worktree)

	recs, err := store.LoadWorktreeProcs()
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	if _, ok := recs[plain.ID]; ok {
		t.Fatalf("a pane inside its own project was recorded as a worktree process: %+v", recs[plain.ID])
	}
	found, ok := recs[wt.ID]
	if !ok {
		t.Fatalf("the worktree pane was not recorded; got %+v", recs)
	}
	if found.PID != wt.Sess.Pid() || !sameDir(found.Path, worktree) {
		t.Fatalf("recorded %+v, want pid %d, path %s", found, wt.Sess.Pid(), worktree)
	}

	ws.ClosePaneByID(wt.ID)
	recs, err = store.LoadWorktreeProcs()
	if err != nil {
		t.Fatalf("read records after close: %v", err)
	}
	if _, ok := recs[wt.ID]; ok {
		t.Fatal("a closed worktree pane's record was not forgotten")
	}
}

// TestReapStaleWorktreeProcessesEndsALeftoverProcess covers the backstop
// itself: a process a past run recorded and never got the chance to end --
// nothing here ever calls ws.Close or ws.ClosePaneByID on it, which is
// exactly the situation a relaunch, a crash or the machine losing power under
// a run would leave for the next one to find -- is found and ended with
// nothing but the record left behind, and the empty folder it was holding
// open comes free.
func TestReapStaleWorktreeProcessesEndsALeftoverProcess(t *testing.T) {
	isolateConfig(t)
	home := t.TempDir()
	repo := filepath.Join(home, "code", "repo")
	worktree := filepath.Join(home, "code", "repo-agent-fix")
	ws, _, wt := setupWorktreePane(t, home, repo, worktree)
	pid := wt.Sess.Pid()
	if !store.ProcessAlive(pid) {
		t.Fatal("the worktree pane's process was not running to begin with")
	}

	if n := ReapStaleWorktreeProcesses(); n != 1 {
		t.Fatalf("reaped %d processes, want 1", n)
	}
	waitProcessGone(t, pid)

	recs, err := store.LoadWorktreeProcs()
	if err != nil {
		t.Fatalf("read records after reaping: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("got %d records left after reaping, want 0: %+v", len(recs), recs)
	}

	// worktree is not a git repository here, so it is never one git lists as
	// a worktree of anything -- and, empty, its folder should have come free,
	// which is the whole point: a folder like this is what a process left
	// running in it kept locked on Windows even after the pane working there
	// had been closed. Reaping it does not depend on git being able to say
	// anything about the folder at all: only removing it afterwards does.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(worktree); os.IsNotExist(err) {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the empty leftover folder %s was not removed", worktree)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The pane's own Sess still believes its process is running; closing the
	// workspace on the way out must not choke on one that is already gone.
	ws.Close()
}

// TestReapStaleWorktreeProcessesLeavesARegisteredWorktreeAlone covers a
// worktree git still lists: ending the stray process left in it is right,
// but removing the folder out from under a checkout that is still registered
// is the worktree panel's own job -- its remove or its prune -- not the
// reaper's to do on its way past.
func TestReapStaleWorktreeProcessesLeavesARegisteredWorktreeAlone(t *testing.T) {
	isolateConfig(t)
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	home := t.TempDir()
	repo := filepath.Join(home, "code", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}

	worktree := filepath.Join(home, "code", "repo-agent-fix")
	if err := gitx.AddFrom(repo, worktree, "agent-fix", ""); err != nil {
		t.Fatalf("add worktree: %v", err)
	}

	ws := newTestWorkspace(t, home)
	if err := ws.OpenProject(repo); err != nil {
		t.Fatalf("open %s: %v", repo, err)
	}
	ws.NewTab(session.KindShell, repo, "lead")
	ws.SplitPaneInWith(layout.Horizontal, Choice{Kind: session.KindShell}, worktree)
	wt := ws.FocusedPane()
	if wt == nil || wt.Sess == nil {
		t.Fatal("the worktree pane did not start")
	}
	pid := wt.Sess.Pid()

	if n := ReapStaleWorktreeProcesses(); n != 1 {
		t.Fatalf("reaped %d processes, want 1", n)
	}
	waitProcessGone(t, pid)

	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("stat %s: %v, want a worktree git still lists to be left alone", worktree, err)
	}

	ws.Close()
}

// TestNewLeavesAnotherRunningInstancesProcessesAlone covers `-solo`, which
// starts a second instance beside one that is already up. The registry both
// instances record their panes into is shared -- there is only one state
// directory -- so a record naming a process still running does not by itself
// mean this launch's own sweep may end it: it may be the other instance's own
// pane, doing exactly what it should. Only once no other instance is
// recorded as running does a live record mean what a stale one would.
func TestNewLeavesAnotherRunningInstancesProcessesAlone(t *testing.T) {
	isolateConfig(t)
	home := t.TempDir()
	wtPath := filepath.Join(home, "code", "repo-inuse")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0])
	cmd.Dir = wtPath
	cmd.Env = append(os.Environ(), reapSleepEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the other instance's process: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
		_ = cmd.Wait()
	})
	if err := store.TrackWorktreeProc("other-instance-pane", pid, time.Now(), wtPath); err != nil {
		t.Fatalf("track: %v", err)
	}

	// A running instance recorded under a process id that is not this test
	// binary's own, and is in fact still running (this same stand-in process
	// serves both purposes here, standing in for the other instance's server
	// as well as its pane).
	if err := store.SaveInstance(&store.Instance{PID: pid, URL: "http://127.0.0.1:0/", Token: "t", Started: time.Now()}); err != nil {
		t.Fatalf("save instance: %v", err)
	}
	t.Cleanup(func() { _ = store.ClearInstance() })

	ws := newTestWorkspace(t, home)
	t.Cleanup(ws.Close)

	time.Sleep(200 * time.Millisecond)
	if !store.ProcessAlive(pid) {
		t.Fatal("a new launch ended a process belonging to another instance it found still running")
	}
	recs, err := store.LoadWorktreeProcs()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := recs["other-instance-pane"]; !ok {
		t.Error("the other instance's record was removed even though its process was left alone")
	}
}

// reapSleepEnv, when set in this test binary's own environment, makes it
// stand in for a pane's process left running in a worktree: it does nothing
// but wait, exactly as a stray shell or agent process does once whatever
// started it is gone.
const reapSleepEnv = "FLOCKDECK_WORKSPACE_TEST_REAP_SLEEP"

func init() {
	if os.Getenv(reapSleepEnv) != "" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
}
