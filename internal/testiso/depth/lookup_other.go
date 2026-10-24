//go:build !linux && !darwin && !windows

package depth

// osLookup is nil where the process tree is not read, so Guard does nothing.
func osLookup() Lookup { return nil }
