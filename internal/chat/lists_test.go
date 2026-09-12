package chat

import (
	"strings"
	"testing"
)

// A list is read by its shape: an item nested under another is indented, and
// an item too long for one line goes on under its own text. Dropping the
// indentation flattened every nested list, and wrapping to the margin buried
// each marker in a block of text.
func TestListsKeepTheirShapeWhenWrapped(t *testing.T) {
	var out strings.Builder
	p := newPrinter(&out, 30, false)
	p.text("- a list item that is long enough to wrap around\n  - nested item here\n10. numbered item that also wraps\n")
	p.endMessage()

	want := []string{
		"- a list item that is long",
		"  enough to wrap around",
		"  - nested item here",
		"10. numbered item that also",
		"    wraps",
	}
	got := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("drawn as\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An indented code block's indentation belongs to the block, and the prose
// after it starts at the margin again.
func TestProseAfterAnIndentedCodeBlockIsNotIndented(t *testing.T) {
	var out strings.Builder
	p := newPrinter(&out, 60, false)
	p.text("- step one:\n  ```sh\n  go test\n  ```\nThen it passes.\n")
	p.endMessage()
	if !strings.Contains(out.String(), "\nThen it passes.") {
		t.Errorf("the prose after the block was indented:\n%s", out.String())
	}
}

func TestIsListMarker(t *testing.T) {
	for word, want := range map[string]bool{
		"-": true, "*": true, "1.": true, "12)": true,
		"--": false, "**": false, "1": false, "a.": false, "1.5": false, ".": false,
	} {
		if got := isListMarker(word); got != want {
			t.Errorf("isListMarker(%q) = %v, want %v", word, got, want)
		}
	}
}
