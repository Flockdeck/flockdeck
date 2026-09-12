//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// showStartupError puts a failure to start in front of somebody with nowhere
// else to read it.
//
// The Windows build is linked for the GUI subsystem so that a double-click
// does not open a terminal behind the window, and started that way it has no
// standard error at all. A Flockdeck that could not start then simply never
// appeared. Started from a terminal it does have one, and the message has
// already been printed there, so no box is shown.
func showStartupError(text string) {
	if _, err := os.Stderr.Stat(); err == nil {
		return
	}
	msg, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	title, _ := syscall.UTF16PtrFromString("Flockdeck")
	const iconError = 0x10 // MB_ICONERROR
	_, _, _ = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(
		0, uintptr(unsafe.Pointer(msg)), uintptr(unsafe.Pointer(title)), iconError)
}
