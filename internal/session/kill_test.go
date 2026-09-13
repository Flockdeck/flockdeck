package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// treeEnv makes this test binary, run as a pane's process, stand in for one
// that starts something and leaves it running: "parent" starts a child in a
// way a plain kill of the parent does not reach, writes the child's process id
// to the file treePIDEnv names, and waits; "child" ignores the hangup and
// waits. The child is this binary again, or the program treeChildEnv names.
const (
	treeEnv      = "FLOCKDECK_SESSION_TEST_TREE"
	treePIDEnv   = "FLOCKDECK_SESSION_TEST_TREE_PID"
	treeChildEnv = "FLOCKDECK_SESSION_TEST_TREE_CHILD"
)

func init() {
	switch os.Getenv(treeEnv) {
	case "parent":
		exe := os.Getenv(treeChildEnv)
		if exe == "" {
			exe = os.Args[0]
		}
		child := exec.Command(exe)
		child.Env = append(os.Environ(), treeEnv+"=child")
		// Somewhere other than the pane's folder, which the test removes.
		child.Dir = os.TempDir()
		detach(child)
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

// startTree starts a pane running this binary as "parent", whose child is the
// program child names ("" for this binary again), and returns the pane and the
// child's process id. The child is ended when the test is, whatever it finds.
// env is added to the pane's environment.
func startTree(t *testing.T, child string, env ...string) (*Session, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	env = append(append(Env(), treeEnv+"=parent", treePIDEnv+"="+pidFile), env...)
	if child != "" {
		env = append(env, treeChildEnv+"="+child)
	}
	s, err := Start(Config{ID: "tree", Kind: KindShell, Cwd: t.TempDir(), Argv: []string{os.Args[0]}, Env: env, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	pid := waitForPID(t, s, pidFile)
	if !alive(pid) {
		t.Fatal("the child had gone before the pane was closed")
	}
	return s, pid
}

// waitForPID waits for something running in the pane s to write a process id
// to file, and returns it. That process is ended when the test is, whatever
// it finds.
func waitForPID(t *testing.T, s *Session, file string) int {
	t.Helper()
	pid := 0
	for deadline := time.Now().Add(30 * time.Second); pid == 0; {
		if raw, err := os.ReadFile(file); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		if pid == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("no process id was written to %s; the pane printed:\n%s", file, s.RecentText(4096))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	t.Cleanup(func() { endProcess(pid) })
	return pid
}

// TestClosingAPaneEndsWhatItStarted covers closing a pane whose process has
// started something of its own: a shell's background job, a server an agent
// left running, the node process behind an agent's .cmd shim. Only the pane's
// own process was killed, and the rest ran on -- on Windows holding the pane's
// folder open, so a worktree could not be removed after its pane was closed.
func TestClosingAPaneEndsWhatItStarted(t *testing.T) {
	s, child := startTree(t, "")
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// On Windows Close waits for all of it. Elsewhere a killed process is
	// reaped by whoever inherited it, a moment later.
	wait := 5 * time.Second
	if runtime.GOOS == "windows" {
		wait = 0
	}
	for deadline := time.Now().Add(wait); alive(child); time.Sleep(20 * time.Millisecond) {
		if !time.Now().Before(deadline) {
			t.Fatal("a process the pane started outlived closing the pane")
		}
	}
}
