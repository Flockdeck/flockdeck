//go:build !race

package record

// slow scales the time limits of tests that time the code: the race detector makes
// it many times slower, and these tests are about growth, not speed.
const slow = 1
