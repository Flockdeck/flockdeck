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

// detachFromTerminal has nothing to do here: a shell does not wait for a GUI
// program, so the prompt is back at once, and releaseConsole lets the
// terminal go as start-up ends, after which closing it does not end the run.
func detachFromTerminal() (done bool, code int) { return false, 0 }

// letTerminalGo is never needed here: Windows sends no SIGHUP.
func letTerminalGo() {}

// detachConsole lets go of the console a run detaching from its window was
// started in. Closing a console ends every program attached to it, whatever
// they make of the event, so a console build started from a terminal — the
// one `go install` makes, or -no-window — went down with its agents when that
// terminal closed, detached or not. What it prints from then on goes nowhere,
// unless it was redirected somewhere that is still there. The release has let
// its borrowed terminal go already, and this finds nothing to do.
func detachConsole() {
	if borrowed.held {
		releaseConsole()
		return
	}
	var mode uint32
	isConsole := func(f *os.File) bool { return syscall.GetConsoleMode(syscall.Handle(f.Fd()), &mode) == nil }
	if isConsole(os.Stdout) || isConsole(os.Stderr) {
		null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err == nil {
			if isConsole(os.Stdout) {
				os.Stdout = null
			}
			if isConsole(os.Stderr) {
				os.Stderr = null
			}
			log.SetOutput(os.Stderr)
		}
	}
	_, _, _ = procFreeConsole.Call()
}

// ctrlCStops reports whether Ctrl+C typed in the terminal reaches this run.
// It does for a console build, whose console is its own, and not for the
// release, which borrows its terminal's: with the prompt given back to the
// shell, the key goes to the shell alone.
func ctrlCStops() bool { return !borrowed.held }

func openConsole(name string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	return syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
}
