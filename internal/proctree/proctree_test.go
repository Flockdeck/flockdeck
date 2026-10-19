package proctree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// treeEnv makes this test binary stand in for an agent that starts a child of
// its own, the way the Codex adapter starts its app-server: "parent" starts a
// child, writes the child's process id to the file treePIDEnv names and waits;
// "child" ignores the hangup and waits.
const (
	treeEnv    = "FLOCKDECK_PROCTREE_TEST"
	treePIDEnv = "FLOCKDECK_PROCTREE_TEST_PID"
)

func init() {
	switch os.Getenv(treeEnv) {
	case "parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), treeEnv+"=child")
		child.Dir = os.TempDir()
		if err := child.Start(); err != nil {
			fmt.Println("start the child:", err)
			os.Exit(2)
		}
		file := os.Getenv(treePIDEnv)
		if err := os.WriteFile(file+".tmp", []byte(strconv.Itoa(child.Process.Pid)), 0o600); err == nil {
			_ = os.Rename(file+".tmp", file)
		}
		time.Sleep(time.Hour)
		os.Exit(0)
	case "child":
		ignoreHangups()
		time.Sleep(time.Hour)
		os.Exit(0)
	}
}

// started is a parent process under a Tree, and the child it started.
type started struct {
	cmd    *exec.Cmd
	tree   *Tree
	reaped chan struct{}
	child  int
}

// startParent starts this binary as "parent" with no pane, as an agent client
// does, and waits until it has started its child. Both are ended when the
// test is, whatever the test did.
func startParent(t *testing.T) *started {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command(os.Args[0])
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), treeEnv+"=parent", treePIDEnv+"="+pidFile)
	tree, err := Start(cmd)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	s := &started{cmd: cmd, tree: tree, reaped: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(s.reaped)
	}()
	t.Cleanup(func() {
		endProcess(cmd.Process.Pid)
		if s.child != 0 {
			endProcess(s.child)
		}
	})

	for deadline := time.Now().Add(30 * time.Second); s.child == 0; time.Sleep(20 * time.Millisecond) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			s.child, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		if s.child == 0 && time.Now().After(deadline) {
			t.Fatal("the parent did not report its child")
		}
	}
	if !alive(cmd.Process.Pid) || !alive(s.child) {
		t.Fatal("the parent or its child had gone before the tree was ended")
	}
	return s
}

// waitGone waits for the processes to have ended.
func waitGone(t *testing.T, what string, pids ...int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		running := false
		for _, pid := range pids {
			running = running || alive(pid)
		}
		if !running {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%s: a process was still running after 5 seconds (pids %v)", what, pids)
		}
	}
}

// TestEndEndsTheParentAndItsChild is the point of the package: an agent
// started with no pane, whose child a plain kill of the agent does not reach.
func TestEndEndsTheParentAndItsChild(t *testing.T) {
	s := startParent(t)
	s.tree.End(s.cmd.Process, s.reaped, 3*time.Second)
	waitGone(t, "End", s.cmd.Process.Pid, s.child)
}

// TestEndTwiceAndEndOfNothing covers a second End, as a second Close of a
// client makes, and End of a Tree that tracks nothing.
func TestEndTwiceAndEndOfNothing(t *testing.T) {
	s := startParent(t)
	s.tree.End(s.cmd.Process, s.reaped, 3*time.Second)
	s.tree.End(s.cmd.Process, s.reaped, 3*time.Second)
	waitGone(t, "End twice", s.cmd.Process.Pid, s.child)

	var none *Tree
	none.End(s.cmd.Process, s.reaped, time.Second)
}

// TestKillTreeEndsAProcessNoOneHoldsATreeFor covers the startup sweep: a
// process found again only by its id.
func TestKillTreeEndsAProcessNoOneHoldsATreeFor(t *testing.T) {
	s := startParent(t)
	KillTree(s.cmd.Process.Pid)
	waitGone(t, "KillTree", s.cmd.Process.Pid, s.child)
}

// TestParentsSeesTheChild pins that the process table can be read where the
// platform supports it, and that it names a started child's parent.
func TestParentsSeesTheChild(t *testing.T) {
	if Parents() == nil {
		t.Skip("the process table cannot be read on this platform")
	}
	s := startParent(t)
	if got := Parents()[s.child]; got != s.cmd.Process.Pid {
		t.Errorf("the child's parent is %d, want %d", got, s.cmd.Process.Pid)
	}
}

// TestEndAfterTheProcessHasExitedTouchesNothingElse covers a second End, and
// End on a Tree that tracks nothing, once the process is reaped. The process's
// id may by then be another process's, and the process table must not be
// walked from it: that process's children were ended in its place.
func TestEndAfterTheProcessHasExitedTouchesNothingElse(t *testing.T) {
	a := startParent(t)
	a.tree.End(a.cmd.Process, a.reaped, 3*time.Second)
	waitGone(t, "the first End", a.cmd.Process.Pid, a.child)

	// Another tree, standing in for what a reused id would belong to.
	b := startParent(t)

	// Twice after exit. What keeps this from touching b is the reaped check
	// (and, on Windows, the ended flag); this call does not tell them apart,
	// and the zero Tree below is the one that exercises the id guard alone.
	a.tree.End(a.cmd.Process, a.reaped, 3*time.Second)
	a.tree.End(a.cmd.Process, a.reaped, 3*time.Second)
	if !alive(b.cmd.Process.Pid) || !alive(b.child) {
		t.Fatal("End on an ended Tree ended another tree")
	}

	// A Tree that tracks nothing, told its process was reaped: only the
	// process it was given is ended, not what the process table shows under it.
	closed := make(chan struct{})
	close(closed)
	(&Tree{}).End(b.cmd.Process, closed, 3*time.Second)
	waitGone(t, "End of the process itself", b.cmd.Process.Pid)
	if !alive(b.child) {
		t.Fatal("End of a Tree that tracks nothing walked the process table from a reaped id")
	}
}
