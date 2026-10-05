//go:build !windows

package helpers

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// A helper is started as the leader of a process group of its own, so that a
// signal to the group reaches the launcher and everything it started, and so
// that stopping Flockdeck's own terminal group never reaches a helper by
// accident.
type procGroup struct {
	pid      int
	released atomic.Bool
}

func configureProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachProc(cmd *exec.Cmd, _ func(int) *exec.Cmd) *procGroup {
	return &procGroup{pid: cmd.Process.Pid}
}

var errReleased = errors.New("the process group is gone")

// interrupt sends SIGINT to the group: the signal a terminal's Ctrl+C sends,
// which lens drains its work on.
func (g *procGroup) interrupt() error {
	if g.released.Load() {
		return errReleased
	}
	return syscall.Kill(-g.pid, syscall.SIGINT)
}

// kill sends SIGKILL to the group.
func (g *procGroup) kill() {
	if g.released.Load() {
		return
	}
	_ = syscall.Kill(-g.pid, syscall.SIGKILL)
}

// drain waits until nothing is left in the group, or the deadline.
func (g *procGroup) drain(deadline time.Time) {
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-g.pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// release ends what is left of the group once the launcher has exited and been
// reaped. While anything is in the group its id cannot be handed out again, so
// a group that still answers is the helper's own. If the leader's id is a
// running process again, the group is somebody else's and is left alone.
func (g *procGroup) release() {
	if g.released.Swap(true) {
		return
	}
	if err := syscall.Kill(g.pid, 0); errors.Is(err, syscall.ESRCH) {
		_ = syscall.Kill(-g.pid, syscall.SIGKILL)
	}
}

// terminateStale ends a helper left by an earlier Flockdeck: SIGINT to its
// group, a wait, then SIGKILL. The caller has already checked the process is
// the helper it recorded.
func terminateStale(pid int, grace time.Duration) error {
	if err := syscall.Kill(-pid, syscall.SIGINT); err != nil {
		// Not a group leader after all: signal the one process.
		if err := syscall.Kill(pid, syscall.SIGINT); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
	return nil
}

// commandMentions reports whether the process running as pid has a command
// line containing dir, for a record with no start time to check. Where the
// command cannot be read, the answer is no, and the process is left alone.
func commandMentions(pid int, dir string) bool {
	cmd := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid))
	sysproc.NoWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), dir)
}

// CtrlBreak is Windows only.
func CtrlBreak(pid int) error {
	return errors.New("CTRL_BREAK is only used on Windows")
}

// members lists the processes in the helper's group.
func (g *procGroup) members() ([]int, bool) { return groupMembers(g.pid) }
