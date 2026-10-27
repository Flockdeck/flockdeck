//go:build windows

package main

import (
	"os"
	"syscall"
)

// stopSignals: Go maps a console close, a logoff and a shutdown to SIGTERM. The
// job object the client puts the adapter in ends claude as well, but only when
// the adapter exits, and closing the sessions first is cleaner.
func stopSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
