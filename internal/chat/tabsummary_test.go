package chat

import (
	"strings"
	"testing"
)

// The line drawn under a call has no tab in it: a tab is as wide as the
// terminal's next tab stop, and the line would be wider than measured.
func TestASummaryLineHasNoTabs(t *testing.T) {
	got := summarise("1\tpackage chat\n2\t\n3\timport \"fmt\"\n", 40)
	if strings.Contains(got, "\t") {
		t.Errorf("summarise = %q, which has a tab in it", got)
	}
	if !strings.HasPrefix(got, "1 package chat") {
		t.Errorf("summarise = %q, want it to lead with the first line, a space for the tab", got)
	}
	if strings.Contains(clipTo("a\tb", 20), "\t") {
		t.Error("clipTo kept a tab")
	}
}
