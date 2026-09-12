package tool

import (
	"strings"
	"testing"
)

// A command's colours and cursor movements are taken out of what it printed:
// the model reads them as noise, and the pane would draw them as marks.
func TestCommandOutputLosesItsEscapeSequences(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[32mPASS\x1b[0m ok":                   "PASS ok",
		"\x1b[1;31merror\x1b[m: bad":               "error: bad",
		"\x1b]0;title\x07done":                     "done",
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\": "link",
		"progress\x1b[2K\x1b[1Gfinished":           "progressfinished",
		"plain text, tabs\tand all":                "plain text, tabs\tand all",
	} {
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}
	if strings.Contains(stripANSI("a\x1b[31"), "\x1b[31") && !strings.Contains(stripANSI("a\x1b[31"), "a") {
		t.Error("a sequence cut off at the end lost the text before it")
	}
}
