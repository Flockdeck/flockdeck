//go:build !windows

package gitx

import "os/exec"

// procTree does nothing here. The git on PATH is git itself, which the
// deadline kills and exec then waits for; see tree_windows.go for the platform
// where that was not so.
type procTree struct{}

func newTree() *procTree { return &procTree{} }

func (t *procTree) start(cmd *exec.Cmd) error { return cmd.Start() }

func (t *procTree) end() {}

// settle has nothing left to wait for: exec has already waited for git.
func (t *procTree) settle(bool) <-chan struct{} { return alreadyGone }
