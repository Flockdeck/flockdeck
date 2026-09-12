//go:build linux || darwin

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/jmwri/flockdeck/internal/sysproc"
)

// detachedEnv marks the copy of the program that -detach starts, so that it
// runs rather than starting another copy in turn.
const detachedEnv = "FLOCKDECK_DETACHED"

// detached is whether this is that copy.
var detached bool

// useConsole has nothing to do here: a program's standard handles do not
// depend on how it was linked.
func useConsole() {}

// ctrlCStops reports whether Ctrl+C typed in the terminal reaches this run,
// which it always does here.
func ctrlCStops() bool { return true }

// detachFromTerminal is how -detach keeps its promise here.
//
// A run started from a terminal belongs to the terminal's session: it held
// the prompt for as long as it ran, and closing the terminal ended it, the
// opposite of what -detach says it does. So the program starts itself again
// in a session of its own and passes on whatever that copy prints while it
// starts. It gives the prompt back once the copy lets go of the terminal, or
// ends with the copy's status when the copy cannot start, or has joined an
// instance already running and is done.
//
// done is false in the copy, which is to carry on and run.
func detachFromTerminal() (done bool, code int) {
	if os.Getenv(detachedEnv) != "" {
		// Not handed on to the panes, so that -detach run in one of them
		// detaches too.
		_ = os.Unsetenv(detachedEnv)
		detached = true
		// The program that started this one may be gone before start-up is
		// over, and printing to it then must not end the run.
		signal.Ignore(syscall.SIGPIPE)
		return false, 0
	}
	if startedAs == "" {
		fmt.Fprintln(os.Stderr, "flockdeck: could not tell where the program is, so -detach stays in this terminal")
		return false, 0
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "flockdeck: -detach:", err)
		return true, 1
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "flockdeck: -detach:", err)
		return true, 1
	}
	cmd := exec.Command(startedAs, os.Args[1:]...)
	cmd.Env = append(os.Environ(), detachedEnv+"=1")
	cmd.Stdout, cmd.Stderr = outW, errW
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	sysproc.NoWindow(cmd)
	err = cmd.Start()
	outW.Close()
	errW.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "flockdeck: -detach: start in the background:", err)
		return true, 1
	}

	copied := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(os.Stdout, outR); copied <- struct{}{} }()
	go func() { _, _ = io.Copy(os.Stderr, errR); copied <- struct{}{} }()
	<-copied
	<-copied

	// Both are closed once the copy has let go of them, which it does as it
	// settles down to run, or when it ends.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit) && exit.ExitCode() > 0:
			return true, exit.ExitCode()
		case err != nil:
			return true, 1
		}
		return true, 0
	case <-time.After(time.Second):
		return true, 0
	}
}

// releaseConsole lets the terminal go once start-up is over. Only the copy
// -detach started has one to let go of: what it prints goes to the program
// that started it, which gives the prompt back when both are closed.
func releaseConsole() {
	if detached {
		outputsToNull()
	}
}

// detachConsole has nothing to do here at the moment of detaching: the
// terminal is let go when it closes, by letTerminalGo.
func detachConsole() {}

// letTerminalGo is what a run detached from the window does when the
// terminal it was started from closes. It carries on, since detaching
// promised its agents would, and what it prints from then on goes nowhere,
// rather than to a terminal that has gone or a pipe whose end could end it.
func letTerminalGo() {
	signal.Ignore(syscall.SIGPIPE)
	outputsToNull()
}

// outputsToNull points standard output and error at /dev/null. They are
// pointed rather than closed, so that nothing opened later is handed their
// numbers and written to by mistake.
func outputsToNull() {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer null.Close()
	_ = dupOnto(int(null.Fd()), 1)
	_ = dupOnto(int(null.Fd()), 2)
}
