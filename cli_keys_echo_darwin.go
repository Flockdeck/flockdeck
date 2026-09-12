package main

import "syscall"

// The requests that read and set a terminal's settings, which each system
// names differently.
const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)
