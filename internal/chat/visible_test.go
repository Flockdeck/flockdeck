package chat

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Escape sequences from outside -- a file the model read, a command's output,
// the model's own answer -- are drawn as marks rather than acted on by the
// terminal.
func TestControlCharactersFromOutsideAreDrawnVisibly(t *testing.T) {
	const clipboard = "\x1b]52;c;ZXZpbA==\x07"
	var out strings.Builder
	p := newPrinter(&out, 70, true)
	p.text("the file says " + clipboard + "hello\n")
	p.endMessage()
	p.line(ansiDim, "  \x1b[31mred with no end")
	p.line("", "progress 10%\rprogress 100%\x08\u009b2J")
	p.line("", "an eight-bit CSI on its own: \x9b2J")

	got := out.String()
	for _, bad := range []string{"\x1b]52", "\x07", "\x1b[31m", "\x08", "\u009b", "\r"} {
		if strings.Contains(got, bad) {
			t.Errorf("drew %q to the terminal:\n%q", bad, got)
		}
	}
	// A byte that is not UTF-8 -- 0x9b alone -- is not passed on either.
	if !utf8.ValidString(got) {
		t.Errorf("drew bytes that are not UTF-8:\n%q", got)
	}
	for _, want := range []string{"␛]52;c;ZXZpbA==", "␛[31mred", "progress 10%\nprogress 100%", "on its own: \uFFFD2J"} {
		if !strings.Contains(got, want) {
			t.Errorf("did not draw %q:\n%q", want, got)
		}
	}
	// The printer's own styling is still drawn.
	if !strings.Contains(got, ansiDim) {
		t.Errorf("the notice lost its own style:\n%q", got)
	}
}
