//go:build !windows && !linux

package session

import "github.com/jmwri/flockdeck/internal/proctree"

// Everywhere else -- macOS above all -- there is no way to walk the process
// table from the standard library alone, and the module's dependencies are
// fixed. Returning nothing is the honest answer: the interface shows no
// reading rather than a wrong one, and everything else about a pane works.

func procParents() map[int]int { return proctree.Parents() }

func procMetrics(int) (procMetric, bool) { return procMetric{}, false }
