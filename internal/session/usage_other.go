//go:build !windows && !linux

package session

// Everywhere else -- macOS above all -- there is no way to walk the process
// table from the standard library alone, and the module's dependencies are
// fixed. Returning nothing is the honest answer: the interface shows no
// reading rather than a wrong one, and everything else about a pane works.

func procParents() map[int]int { return nil }

func procMetrics(int) (procMetric, bool) { return procMetric{}, false }

// sessionMembers cannot be listed here either, so closing a pane signals only
// its own process group; see endTree.
func sessionMembers(int) []int { return nil }
