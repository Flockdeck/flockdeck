//go:build !windows

package main

import (
	"os"
	"syscall"
)

// stopSignals are the signals that end the adapter cleanly: its claude
// processes lead sessions of their own and do not get them, so an adapter that
// died without closing its sessions would leave them running.
func stopSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} }
