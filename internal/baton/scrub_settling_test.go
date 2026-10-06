package baton

import (
	"testing"
)

func TestSettlingCases(t *testing.T) {
	sc := NewScrubber()
	for _, in := range []string{
		"token: ].pem",
		"Authorization: Authorization: Hunter2Hunter2xQ9Zk39dLq02Mnb81xYtr",
		"[REDACTED: the x and more",
		"password=[REDACTED: the x",
	} {
		once, _ := sc.Scrub(in)
		twice, _ := sc.Scrub(once)
		if once != twice {
			t.Errorf("Scrub(%q): once %q, twice %q", in, once, twice)
		}
	}
	// An opener made harmless keeps the space after it.
	got, _ := sc.Scrub("a [REDACTED: the x b")
	if got != "a (REDACTED: the x b" {
		t.Errorf("got %q", got)
	}
}

// The generated lines of the gate, scrubbed twice, are scrubbed once.
func TestScrubbingTheGateLinesTwiceIsScrubbingThemOnce(t *testing.T) {
	sc := NewScrubber()
	bad := 0
	for i, c := range gateCases() {
		if i%2 == 1 {
			continue
		}
		once, _ := sc.Scrub(c.line)
		twice, _ := sc.Scrub(once)
		if once != twice {
			bad++
			if bad <= 5 {
				t.Errorf("Scrub(%q): once %q, twice %q", c.line, once, twice)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d lines are not stable", bad)
	}
}
