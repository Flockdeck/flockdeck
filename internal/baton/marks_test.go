package baton

import (
	"testing"
)

func TestFindMarksAgreesWithTheMarkPattern(t *testing.T) {
	for _, s := range []string{
		"a [REDACTED: aws-key] b [REDACTED: nope] c [REDACTED: high-entropy][REDACTED: jwt]",
		"[REDACTED: [REDACTED: jwt]", "[REDACTED: ", "[REDACTED: aws-key", "x[REDACTED: env-secret]y",
	} {
		want := markRe.FindAllStringIndex(s, -1)
		got := findMarks(s)
		if len(want) != len(got) {
			t.Errorf("%q: %v, want %v", s, got, want)
			continue
		}
		for i := range want {
			if want[i][0] != got[i][0] || want[i][1] != got[i][1] {
				t.Errorf("%q: %v, want %v", s, got, want)
			}
		}
	}
}
