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
// that starts something and leaves it running: "parent" starts this binary
// again as "child" in a way a plain kill of the parent does not reach, writes
// the child's process id to the file treePIDEnv names, and waits; "child"
// ignores the hangup and waits. Both are here to be killed.
const (
	treeEnv    = "FLOCKDECK_SESSION_TEST_TREE"
	treePIDEnv = "FLOCKDECK_SESSION_TEST_TREE_PID"
)

func init() {
	switch os.Getenv(treeEnv) {
	case "parent":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), treeEnv+"=child")
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

// TestClosingAPaneEndsWhatItStarted covers closing a pane whose process has
// started something of its own: a shell's background job, a server an agent
// left running, the node process behind an agent's .cmd shim. Only the pane's
// own process was killed, and the rest ran on -- on Windows holding the pane's
// folder open, so a worktree could not be removed after its pane was closed.
func TestClosingAPaneEndsWhatItStarted(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s, err := Start(Config{
		ID:   "tree",
		Kind: KindShell,
		Cwd:  t.TempDir(),
		Argv: []string{os.Args[0]},
		Env:  append(Env(), treeEnv+"=parent", treePIDEnv+"="+pidFile),
		Cols: 80,
		Rows: 24,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	child := 0
	for deadline := time.Now().Add(30 * time.Second); child == 0; {
		if raw, err := os.ReadFile(pidFile); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
		if child == 0 {
			if time.Now().After(deadline) {
				t.Fatalf("the pane's process never started its child; it printed:\n%s", s.RecentText(4096))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// Whatever this test finds, the child does not outlive it.
	t.Cleanup(func() {
		if p, err := os.FindProcess(child); err == nil {
			_ = p.Kill()
		}
	})
	if !alive(child) {
		t.Fatal("the child had gone before the pane was closed")
	}

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
