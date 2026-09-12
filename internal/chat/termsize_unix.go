//go:build linux || darwin

package chat

import (
	"os"
	"syscall"
	"unsafe"
)

// enableColour has nothing to turn on: a terminal here draws escape sequences.
func enableColour() bool { return true }

// terminalWidth is how many columns the terminal standard output is drawn in
// has, or 0 when it is not a terminal.
func terminalWidth() int {
	var ws struct{ Row, Col, X, Y uint16 }
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws))); errno != 0 {
		return 0
	}
	return int(ws.Col)
}
