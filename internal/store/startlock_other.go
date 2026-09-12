//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package store

// lockFile takes no lock here, where there is no flock to take it with; a
// launch goes on as every launch did before there was one.
func lockFile(path string) (unlock func(), err error) { return func() {}, nil }
