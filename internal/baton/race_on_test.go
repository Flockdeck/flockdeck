//go:build race

package baton

// raceEnabled says the race detector is on: it slows the scrubber several times over, so the
// tests that hold it to a time limit do not run, and those that check what it removes run on a
// smaller input.
const raceEnabled = true

// scaled is n, or an eighth of it under the race detector.
func scaled(n int) int { return max(n/8, 1) }
