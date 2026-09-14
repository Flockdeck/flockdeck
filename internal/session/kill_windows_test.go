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

// The windowed program starts the console program windowedChildEnv names, if
// it is set, as this binary's "child", and writes its process id to the file
// windowedPIDEnv names: an editor's terminal or language server.
const (
	windowedChildEnv = "FLOCKDECK_SESSION_TEST_WINDOWED_CHILD"
	windowedPIDEnv   = "FLOCKDECK_SESSION_TEST_WINDOWED_PID"
)

const windowedSource = `package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if exe := os.Getenv("` + windowedChildEnv + `"); exe != "" {
		child := exec.Command(exe)
		child.Env = append(os.Environ(), "` + treeEnv + `=child")
		// CREATE_NO_WINDOW: a console program started by a windowed one
		// otherwise gets a console window of its own.
		child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
		if child.Start() == nil {
			file := os.Getenv("` + windowedPIDEnv + `")
			if os.WriteFile(file+".tmp", []byte(strconv.Itoa(child.Process.Pid)), 0o600) == nil {
				_ = os.Rename(file+".tmp", file)
			}
		}
	}
	time.Sleep(time.Hour)
}
`

// buildWindowedProgram builds a program with windows of its own, in its PE
// header's terms -- it never opens one -- that waits an hour, and returns its
// path. See windowedSource for what else it does.
func buildWindowedProgram(t *testing.T) string {
	t.Helper()
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go command to build a windowed program with: %v", err)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":  "module windowed\n\ngo 1.21\n",
		"main.go": windowedSource,
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

// TestClosingAPaneLeavesWhatItsWindowsStartedRunning covers `code .` in a
// shell pane. The editor stayed open, but the console programs it runs -- the
// terminal in it, its language servers, git -- are in the pane's job too, and
// were ended with the pane, under the editor still on screen.
func TestClosingAPaneLeavesWhatItsWindowsStartedRunning(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	s, _ := startTree(t, buildWindowedProgram(t), windowedChildEnv+"="+os.Args[0], windowedPIDEnv+"="+pidFile)
	grandchild := waitForPID(t, s, pidFile)
	if !alive(grandchild) {
		t.Fatal("the windowed program's child had gone before the pane was closed")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if !alive(grandchild) {
		t.Fatal("a console program a windowed one started was ended with the pane")
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

// TestAJobOutlivesFlockdeckOnlyUntilItsHandleIsClosed covers what happens when
// Flockdeck itself never gets to run endTree at all -- a crash, a `taskkill
// /F`, a shutdown forceQuit gives up waiting on. None of those run a line of
// Go: the operating system simply closes every handle the dying process still
// held, the pane's job among them, and closing it is what has to be enough on
// its own. Without JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE that only cuts the job's
// members loose -- which is the bug this covers: a process detach()ed from
// its pane, exactly like the one a plain kill of the pane's own process
// already fails to reach, going on running with the pane's directory as its
// own after Flockdeck is gone, for as long as it likes.
func TestAJobOutlivesFlockdeckOnlyUntilItsHandleIsClosed(t *testing.T) {
	s, child := startTree(t, "")

	// Standing in for Flockdeck's own process dying abruptly: nothing walks
	// the job first, and nothing clears the limit endTree clears before its
	// own, graceful CloseHandle. The pane's own process is left running too,
	// exactly as it would be the instant Flockdeck's process table entry
	// disappears and every handle it held goes with it in the same moment --
	// there is nothing in between for a real crash either.
	s.mu.Lock()
	job := s.tree.job
	s.tree.job = 0 // so t.Cleanup's s.Close does not act on this job again
	pane := s.cmd.Process.Pid
	s.mu.Unlock()
	if job == 0 {
		t.Fatal("the process could not be put in a job")
	}
	if err := syscall.CloseHandle(job); err != nil {
		t.Fatalf("close the job handle: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for alive(pane) || alive(child) {
		if !time.Now().Before(deadline) {
			t.Fatalf("closing the job handle without a graceful shutdown left pane=%v child=%v running",
				alive(pane), alive(child))
		}
		time.Sleep(20 * time.Millisecond)
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
