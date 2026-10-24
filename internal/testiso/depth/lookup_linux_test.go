package depth

import "testing"

func TestParseStatReadsTheParentTheNameAndTheStartWhateverTheNameHolds(t *testing.T) {
	// Fields after the name: state ppid pgrp session tty tpgid flags minflt cminflt
	// majflt cmajflt utime stime cutime cstime priority nice threads itrealvalue
	// starttime (the 22nd field overall).
	const tail = "S 17 4242 4242 0 -1 4194560 100 0 0 0 1 1 0 0 20 0 1 0 987654 1000"
	for _, tc := range []struct {
		line string
		want Proc
		ok   bool
	}{
		{"4242 (x.test) " + tail, Proc{17, "x.test", 987654}, true},
		{"4242 (a b) c) " + tail, Proc{17, "a b) c", 987654}, true},
		{"4242 (x.test) S 17", Proc{17, "x.test", 0}, true},
		{"4242 (x.test) S", Proc{}, false},
		{"4242 x.test S 17", Proc{}, false},
		{"4242 (x.test) S notanumber 1", Proc{}, false},
	} {
		got, ok := parseStat(tc.line)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q: got %+v %v, want %+v %v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}
