//go:build !windows && !linux

package proctree

// Everywhere else -- macOS above all -- there is no way to walk the process
// table from the standard library alone, and the module's dependencies are
// fixed. Returning nothing is the honest answer.

// Parents returns nothing here.
func Parents() map[int]int { return nil }

// SessionMembers cannot be listed here either, so End signals only the
// process group of the process it ends.
func SessionMembers(int) []int { return nil }
