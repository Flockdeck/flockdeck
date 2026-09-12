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
