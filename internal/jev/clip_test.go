package jev

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A refusal's body is cut for an error message at a byte count, which can fall
// inside a character: the message then ended in bytes that are no text.
func TestClipCutsOnACharacterBoundary(t *testing.T) {
	body := strings.Repeat("é", 200)         // two bytes each, so byte 300 is a boundary...
	for _, lead := range []string{"", "x"} { // ...and with one byte in front, it is not
		got := clip(lead + body)
		if !utf8.ValidString(got) {
			t.Errorf("clip with %q in front is not valid UTF-8: %q", lead, got[len(got)-8:])
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("clip with %q in front does not say it was cut", lead)
		}
	}
}
