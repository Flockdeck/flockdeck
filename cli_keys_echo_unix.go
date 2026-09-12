//go:build linux || darwin

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// hideInput stops the terminal showing what is typed on standard input, and
// returns what puts it back, or nil where it could not.
func hideInput() func() {
	fd := os.Stdin.Fd()
	var was syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetTermios, uintptr(unsafe.Pointer(&was))); errno != 0 {
		return nil
	}
	quiet := was
	quiet.Lflag &^= syscall.ECHO
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlSetTermios, uintptr(unsafe.Pointer(&quiet))); errno != 0 {
		return nil
	}
	return func() {
		syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlSetTermios, uintptr(unsafe.Pointer(&was)))
	}
}
