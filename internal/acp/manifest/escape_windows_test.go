//go:build windows

package manifest

import "os/exec"

// leaveSession does nothing on Windows: the job object holds descendants, and
// the unix test of an escaped one does not apply.
func leaveSession(c *exec.Cmd) {}
