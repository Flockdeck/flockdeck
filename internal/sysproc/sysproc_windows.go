//go:build windows

package sysproc

import (
	"os/exec"
	"syscall"
)

// createNoWindow is CREATE_NO_WINDOW. The child still gets a console -- a
// program that writes to one keeps working, and anything it starts in turn
// shares it rather than being given yet another -- but nobody is shown it.
const createNoWindow = 0x08000000

// NoWindow marks cmd to start without a console window. The caller captures or
// discards its output either way, so a window would only ever show the user a
// flash of text they had no use for.
//
// It is for console programs run in the background. A pane does not need it,
// because it runs in a pseudo-terminal of its own, and neither does the
// browser, which is a GUI program Windows gives no console to in the first
// place. Flags the caller has already set are kept.
func NoWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
