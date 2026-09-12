package chat

import (
	"strings"
	"testing"
)

// The line drawn under a command says whether it worked: its exit status is
// its last line, and the first line of what it printed says nothing of that.
func TestACommandIsSummarisedByItsResult(t *testing.T) {
	got := summarise("=== RUN   TestThing\n--- FAIL: TestThing\nFAIL\n[exit status 1]\n", 80)
	if !strings.HasPrefix(got, "[exit status 1]") || !strings.Contains(got, "4 lines") {
		t.Errorf("summarise = %q, want it to lead with the exit status", got)
	}
	if got := summarise("line one\nline two\n", 80); !strings.HasPrefix(got, "line one") {
		t.Errorf("other output = %q, want it to lead with its first line", got)
	}
}

// A search's result is how much it found, which it says last; its first line
// is only the first match.
func TestASearchIsSummarisedByWhatItFound(t *testing.T) {
	for out, want := range map[string]string{
		"a.go:1: x\nb.go:2: y\n\n2 matches in 2 files.\n":             "2 matches in 2 files.",
		"a.go:1: x\n\n1 match in 1 file.\n":                           "1 match in 1 file.",
		"a.go\nb.go\n[stopped at 1000 matches; narrow the pattern]\n": "[stopped at 1000 matches",
	} {
		if got := summarise(out, 80); !strings.HasPrefix(got, want) {
			t.Errorf("summarise = %q, want it to lead with %q", got, want)
		}
	}
}
