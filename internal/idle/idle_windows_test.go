//go:build windows

package idle

import (
	"testing"
	"time"
)

// sinceTicks must come out right across the 32-bit wrap of GetTickCount: a
// last-input time from just before it, read against a now from just after,
// is a short idle, not a nonsense multi-week one.
func TestSinceTicksWraps(t *testing.T) {
	cases := []struct {
		name           string
		now, lastInput uint32
		want           time.Duration
	}{
		{"no wrap", 10000, 7000, 3000 * time.Millisecond},
		{"across the wrap", 500, 4294966796, 1000 * time.Millisecond}, // 500 - (2^32-500) mod 2^32
		{"exactly now", 10000, 10000, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sinceTicks(c.now, c.lastInput); got != c.want {
				t.Errorf("sinceTicks(%d, %d) = %v, want %v", c.now, c.lastInput, got, c.want)
			}
		})
	}
}

// Since answers on this machine, whatever it says: there is always a last
// input, and GetTickCount always runs.
func TestSinceAnswers(t *testing.T) {
	d, _, ok := Since()
	if !ok {
		t.Fatal("Since did not answer on Windows")
	}
	if d < 0 {
		t.Errorf("a negative idle time: %v", d)
	}
}
