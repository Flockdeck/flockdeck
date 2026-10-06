//go:build !race

package baton

// raceEnabled says the race detector is on (see race_on_test.go).
const raceEnabled = false

// scaled is n, or an eighth of it under the race detector.
func scaled(n int) int { return n }
