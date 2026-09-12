package main

import "syscall"

// The requests that read and set a terminal's settings, which each system
// names differently.
const (
	ioctlGetTermios = syscall.TCGETS
	ioctlSetTermios = syscall.TCSETS
)
