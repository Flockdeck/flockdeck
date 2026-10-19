//go:build windows

package proctree

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// startSleeper starts this binary as a child that waits an hour, outside any
// pane, and returns its process id. It is ended when the test is.
func startSleeper(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), treeEnv+"=child")
	cmd.Dir = os.TempDir()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		endProcess(pid)
		_ = cmd.Wait()
	})
	return pid
}

// TestAnIDTheJobListedIsNotEndedOnceItIsSomebodyElses covers the moment
// between the pane's job listing its processes and each being ended. A
// process listed may end in it, and its id be given to a process of somebody
// else's, which was then opened by that id and terminated.
func TestAnIDTheJobListedIsNotEndedOnceItIsSomebodyElses(t *testing.T) {
	member, outsider := startSleeper(t), startSleeper(t)
	tree := containTree(member)
	if tree.job == 0 {
		t.Fatal("the process could not be put in a job")
	}
	defer syscall.CloseHandle(tree.job)

	h, ok := openMember(tree.job, uint32(member))
	if !ok {
		t.Fatal("a process in the job was not opened to be ended")
	}
	_ = syscall.CloseHandle(h)
	if h, ok := openMember(tree.job, uint32(outsider)); ok {
		_ = syscall.CloseHandle(h)
		t.Fatal("a process that is not in the job was opened to be ended")
	}
}

// TestAJobHoldingMoreThanThereIsRoomForStillListsWhatFits covers a pane
// whose tree has grown past the room its job is listed into. The listing
// fails as ERROR_MORE_DATA, and that was taken for nothing listed, so closing
// the pane ended none of the tree rather than most of it.
func TestAJobHoldingMoreThanThereIsRoomForStillListsWhatFits(t *testing.T) {
	first, second := startSleeper(t), startSleeper(t)
	tree := containTree(first)
	if tree.job == 0 {
		t.Fatal("the process could not be put in a job")
	}
	defer syscall.CloseHandle(tree.job)
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(second))
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := procAssignProcessToJobObject.Call(uintptr(tree.job), uintptr(h))
	_ = syscall.CloseHandle(h)
	if r == 0 {
		t.Fatalf("the second process could not be put in the job: %v", err)
	}

	if all := listJob(tree.job, 2); len(all) != 2 {
		t.Fatalf("a job of two listed with room for two gave %v", all)
	}
	got := listJob(tree.job, 1)
	if len(got) != 1 || (got[0] != uint32(first) && got[0] != uint32(second)) {
		t.Fatalf("a job of two listed with room for one gave %v, want one of %d and %d", got, first, second)
	}
}

// TestConsoleProgramsAreToldFromWindowedOnes pins the reading the choice rests
// on, against programs every Windows machine has.
func TestConsoleProgramsAreToldFromWindowedOnes(t *testing.T) {
	root := os.Getenv("SystemRoot")
	for path, console := range map[string]bool{
		filepath.Join(root, "System32", "cmd.exe"):     true,
		filepath.Join(root, "System32", "notepad.exe"): false,
		filepath.Join(root, "explorer.exe"):            false,
	} {
		sub, ok := peSubsystem(path)
		if !ok {
			t.Errorf("%s: the subsystem could not be read", path)
			continue
		}
		if got := sub != imageSubsystemWindowsGUI; got != console {
			t.Errorf("%s: subsystem %d read as console=%v, want %v", path, sub, got, console)
		}
	}
	if _, ok := peSubsystem(filepath.Join(t.TempDir(), "missing.exe")); ok {
		t.Error("a program that is not there was read")
	}
}

// TestAJobOutlivesFlockdeckOnlyUntilItsHandleIsClosed covers what happens when
// Flockdeck itself never gets to run End at all: a crash, a taskkill /F, a
// shutdown forceQuit gives up waiting on. None of those run a line of Go. The
// operating system closes every handle the dying process still held, the job
// among them, and closing it has to be enough on its own. Without
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE that only cuts the job's members loose,
// and a child goes on running after Flockdeck is gone.
func TestAJobOutlivesFlockdeckOnlyUntilItsHandleIsClosed(t *testing.T) {
	s := startParent(t)

	// Standing in for Flockdeck's own process dying abruptly: nothing walks
	// the job first, and nothing clears the limit End clears before its own
	// graceful CloseHandle.
	s.tree.mu.Lock()
	job := s.tree.tree.job
	s.tree.tree.job = 0 // so a later End does not act on this job again
	s.tree.mu.Unlock()
	if job == 0 {
		t.Fatal("the process could not be put in a job")
	}
	if err := syscall.CloseHandle(job); err != nil {
		t.Fatalf("close the job handle: %v", err)
	}
	waitGone(t, "closing the job handle", s.cmd.Process.Pid, s.child)
}
