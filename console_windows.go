//go:build windows

package main

import (
	"log"
	"os"
	"syscall"
)

var (
	kernel32          = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole = kernel32.NewProc("AttachConsole")
	procFreeConsole   = kernel32.NewProc("FreeConsole")
)

// borrowed is what useConsole replaced, so that releaseConsole can put it
// back.
var borrowed struct {
	held           bool
	out            syscall.Handle
	stdout, stderr *os.File
}

// useConsole prints to the terminal the program was started from.
//
// The Windows build is linked for the GUI subsystem so that a double-click does
// not open a terminal behind the window, and Windows gives a program linked
// that way no console and no standard handles. Typed in a terminal, `flockdeck
// -version`, update, agents, keys, remote and every error printed nothing, and
// the only sign of a mistyped flag was an exit status. This borrows the
// terminal's console for whichever of standard output and error is missing.
// Handles that were redirected are there already and are left alone, and a
// double-click finds no console to borrow and so opens none.
//
// Input is not taken: a shell does not wait for a GUI program, and goes on
// reading the keyboard itself, so the two would compete for what is typed
// next, a key included.
func useConsole() {
	missing := func(f *os.File) bool { _, err := f.Stat(); return err != nil }
	if !missing(os.Stdout) && !missing(os.Stderr) {
		return
	}
	const attachParentProcess = ^uint32(0) // ATTACH_PARENT_PROCESS
	if r, _, _ := procAttachConsole.Call(uintptr(attachParentProcess)); r == 0 {
		return
	}
	out, err := openConsole("CONOUT$")
	if err != nil {
		_, _, _ = procFreeConsole.Call()
		return
	}
	borrowed.held, borrowed.out, borrowed.stdout, borrowed.stderr = true, out, os.Stdout, os.Stderr
	if missing(os.Stdout) {
		os.Stdout = os.NewFile(uintptr(out), "CONOUT$")
	}
	if missing(os.Stderr) {
		os.Stderr = os.NewFile(uintptr(out), "CONOUT$")
	}
	// The standard logger kept the standard error it was made with.
	log.SetOutput(os.Stderr)
}

// releaseConsole lets a borrowed console go before Flockdeck settles down to
// run behind its window. Every program attached to a console is sent its
// Ctrl+C and is closed along with it, so otherwise a Ctrl+C typed at the
// prompt later, or closing the terminal it happened to be started from, would
// stop Flockdeck and every agent in it. From here on a failure is reported the
// way a double-clicked one is.
func releaseConsole() {
	if !borrowed.held {
		return
	}
	os.Stdout, os.Stderr = borrowed.stdout, borrowed.stderr
	log.SetOutput(os.Stderr)
	_ = syscall.CloseHandle(borrowed.out)
	_, _, _ = procFreeConsole.Call()
	borrowed.held = false
}

func openConsole(name string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	return syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
}
