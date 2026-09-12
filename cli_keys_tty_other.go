//go:build !windows

package main

import "os"

// isMsysTerminal is only a question on Windows, where mintty gives a program
// pipes rather than a terminal.
func isMsysTerminal(*os.File) bool { return false }
