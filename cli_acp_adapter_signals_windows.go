//go:build windows

package main

import "os"

// stopSignals: the job object the client puts the adapter in ends claude with
// it, so an interrupt is all there is to handle.
func stopSignals() []os.Signal { return []os.Signal{os.Interrupt} }
