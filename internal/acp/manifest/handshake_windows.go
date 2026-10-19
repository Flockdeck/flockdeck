//go:build windows

package manifest

import "os/exec"

// contain does nothing here: the canary runs in a throwaway CI machine, and the
// app starts agents under internal/proctree, not through Handshake.
func contain(cmd *exec.Cmd) {}

func kill(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
