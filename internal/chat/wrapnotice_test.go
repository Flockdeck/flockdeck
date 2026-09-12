package chat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A notice longer than the pane is wrapped at spaces rather than broken
// mid-word by the terminal, keeping its indentation; the words stay whole and
// in order.
func TestANoticeIsWrappedToThePane(t *testing.T) {
	var out strings.Builder
	p := newPrinter(&out, 30, false)
	notice := "  (the conversation is longer than the model can read; /clear starts it over)"
	p.line(ansiDim, notice)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("a %d-column notice in a pane of 30 was drawn on one line", len(notice))
	}
	for _, l := range lines {
		if utf8.RuneCountInString(l) > 30 || !strings.HasPrefix(l, "  ") {
			t.Errorf("line %q is wider than the pane or lost its indentation", l)
		}
	}
	if strings.Join(strings.Fields(out.String()), " ") != strings.Join(strings.Fields(notice), " ") {
		t.Errorf("the words changed:\n%s", out.String())
	}
}

// Tool output is shown as it came, however wide.
func TestToolOutputIsNotWrapped(t *testing.T) {
	var out strings.Builder
	p := newPrinter(&out, 30, false)
	raw := "a line of tool output that is wider than the pane it is drawn in"
	p.line("", raw)
	if out.String() != raw+"\n" {
		t.Errorf("tool output was changed: %q", out.String())
	}
}
