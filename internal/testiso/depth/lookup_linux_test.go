package depth

import "testing"

func TestParseStatReadsTheParentAndTheNameWhateverTheNameHolds(t *testing.T) {
	for _, tc := range []struct {
		line   string
		parent int
		name   string
		ok     bool
	}{
		{"4242 (x.test) S 17 4242 4242 0 -1", 17, "x.test", true},
		{"4242 (a b) c) R 99 1 1 0", 99, "a b) c", true},
		{"4242 (x.test) S", 0, "", false},
		{"4242 x.test S 17", 0, "", false},
		{"4242 (x.test) S notanumber 1", 0, "", false},
	} {
		parent, name, ok := parseStat(tc.line)
		if parent != tc.parent || name != tc.name || ok != tc.ok {
			t.Errorf("%q: got %d %q %v, want %d %q %v", tc.line, parent, name, ok, tc.parent, tc.name, tc.ok)
		}
	}
}
