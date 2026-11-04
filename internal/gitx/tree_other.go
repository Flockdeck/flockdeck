//go:build !windows

package gitx

import (
	"os/exec"
	"sync/atomic"
	"syscall"
)

// procTree ends the processes git started, not git alone. The deadline used to
// kill git and leave a pre-commit hook, an ssh or a credential helper running,
// and the hook's own children after it. git is started in a process group of
// its own, which everything it starts joins unless it makes another, and
// ending the tree kills the group. See tree_windows.go for the platform where
// git on PATH is a wrapper around git.
type procTree struct{ pid atomic.Int32 }

func newTree() *procTree { return &procTree{} }

func (t *procTree) start(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return err
	}
	t.pid.Store(int32(cmd.Process.Pid))
	return nil
}

// end kills the whole group. It is called when git is given up on, before the
// process itself is killed; git not yet recorded as started is left to that.
func (t *procTree) end() {
	if pid := t.pid.Load(); pid > 0 {
		_ = syscall.Kill(-int(pid), syscall.SIGKILL)
	}
}

// settle has nothing left to wait for: exec has already waited for git.
func (t *procTree) settle(bool) <-chan struct{} { return alreadyGone }
