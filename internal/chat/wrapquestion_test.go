package chat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The question put before a tool runs is the line most closely read, and in a
// narrow pane the terminal broke it mid-word. Its sentence is wrapped to the
// pane; the code shown under it is left as it is.
func TestAQuestionIsWrappedToThePaneAndItsCodeIsNot(t *testing.T) {
	var out strings.Builder
	p := newPrinter(&out, 40, false)
	code := "  │ func main() { fmt.Println(\"a line of code wider than the pane\") }"
	p.line(ansiBold, "Overwrite internal/chat/tool/files.go (12.1 kB, 391 lines) with 12.4 kB, 399 lines?\n"+code)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	if last != code {
		t.Errorf("the code under the question was changed: %q", last)
	}
	for _, l := range lines[:len(lines)-1] {
		if utf8.RuneCountInString(l) > 40 {
			t.Errorf("a line of the question is wider than the pane: %q", l)
		}
	}
	if len(lines) < 3 {
		t.Errorf("the question was not wrapped:\n%s", out.String())
	}
}
