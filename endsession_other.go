//go:build !windows

package main

// watchEndSession has nothing to do here: a logoff or shutdown sends SIGTERM,
// which watchSignals already hands to the orderly stop.
func watchEndSession(stop func(), done <-chan struct{}) {}
