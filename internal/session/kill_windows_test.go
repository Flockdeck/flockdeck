//go:build windows

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// detach starts cmd with no console at all, which is what a program started
// to run on in the background looks like: nothing ends it when the pane's
// console goes.
func detach(cmd *exec.Cmd) {
	const detachedProcess = 0x00000008
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP}
}

// ignoreHangups does nothing: Windows sends no hangup.
func ignoreHangups() {}

// alive reports whether a process is still running.
func alive(pid int) bool {
	h, err := syscall.OpenProcess(synchronize|processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	ev, err := syscall.WaitForSingleObject(h, 0)
	return err == nil && ev == syscall.WAIT_TIMEOUT
}

// endProcess ends a process a test started, and waits for it to be gone, so
// that nothing it holds is still held when the test's folders are removed.
func endProcess(pid int) {
	h, err := syscall.OpenProcess(processTerminate|synchronize, false, uint32(pid))
	if err != nil {
		return
	}
	defer syscall.CloseHandle(h)
	_ = syscall.TerminateProcess(h, 1)
	_, _ = syscall.WaitForSingleObject(h, 5000)
}

// buildWindowedProgram builds a program with windows of its own, in its PE
// header's terms -- it never opens one -- that waits an hour, and returns its
// path.
func buildWindowedProgram(t *testing.T) string {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go command to build a windowed program with: %v", err)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":  "module windowed\n\ngo 1.21\n",
		"main.go": "package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(time.Hour) }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(dir, "windowed.exe")
	build := exec.Command(goTool, "build", "-ldflags=-H=windowsgui", "-o", exe, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build a windowed program: %v\n%s", err, out)
	}
	if sub, ok := peSubsystem(exe); !ok || sub != imageSubsystemWindowsGUI {
		t.Fatalf("the program built says subsystem %d (read: %v), want a windowed one", sub, ok)
	}
	return exe
}

// TestClosingAPaneLeavesItsWindowsOpen covers the other side of ending what a
// pane started. An agent opening a link starts the browser when none is
// running yet, and `code .` in a shell pane starts the editor: that program is
// the user's once it is on screen, and closing a terminal does not close it.
// Ending everything in the pane's job closed somebody's whole browser with the
// pane, and again when Flockdeck exited.
func TestClosingAPaneLeavesItsWindowsOpen(t *testing.T) {
	s, child := startTree(t, buildWindowedProgram(t))
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Nothing ends it a moment later either: the job has been let go of too.
	time.Sleep(200 * time.Millisecond)
	if !alive(child) {
		t.Fatal("a windowed program the pane started was closed with the pane")
	}
}

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
