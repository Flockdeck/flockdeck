//go:build windows

package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/proctree"
)

// Access rights the helpers below open a process with.
const (
	processTerminate               = 0x0001
	processQueryLimitedInformation = 0x1000
	synchronize                    = 0x00100000
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
	if windowed, ok := proctree.Windowed(exe); !ok || !windowed {
		t.Fatalf("the program built is not a windowed one (windowed: %v, read: %v)", windowed, ok)
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
