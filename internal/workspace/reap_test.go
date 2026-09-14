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

	procs, err := store.WorktreeProcesses()
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	var found *store.WorktreeProcess
	for i := range procs {
		switch procs[i].PaneID {
		case wt.ID:
			found = &procs[i]
		case plain.ID:
			t.Fatalf("a pane inside its own project was recorded as a worktree process: %+v", procs[i])
		}
	}
	if found == nil {
		t.Fatalf("the worktree pane was not recorded; got %+v", procs)
	}
	if found.PID != wt.Sess.Pid() || !sameDir(found.Cwd, worktree) || !sameDir(found.Repo, repo) {
		t.Fatalf("recorded %+v, want pid %d, cwd %s, repo %s", found, wt.Sess.Pid(), worktree, repo)
	}

	ws.ClosePaneByID(wt.ID)
	procs, err = store.WorktreeProcesses()
	if err != nil {
		t.Fatalf("read records after close: %v", err)
	}
	for _, p := range procs {
		if p.PaneID == wt.ID {
			t.Fatalf("a closed worktree pane's record was not forgotten: %+v", p)
		}
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

	procs, err := store.WorktreeProcesses()
	if err != nil {
		t.Fatalf("read records after reaping: %v", err)
	}
	if len(procs) != 0 {
		t.Fatalf("got %d records left after reaping, want 0: %+v", len(procs), procs)
	}

	// worktree is not a git repository here, so it is never one git lists as
	// a worktree of anything -- and, empty, its folder should have come free,
	// which is the whole point: a folder like this is what a process left
	// running in it kept locked on Windows even after the pane working there
	// had been closed.
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
