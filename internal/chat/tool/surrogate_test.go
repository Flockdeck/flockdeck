package tool

import (
	"bufio"
	"bytes"
	"io"
	"testing"
	"unicode/utf8"
)

// Half of a character left on its own in UTF-16 -- a high surrogate with no
// low one after it, or a low one with no high one before it -- is one
// character that cannot be read, and no more: the character after it is kept.
func TestAnUnpairedSurrogateLosesNoCharacterAfterIt(t *testing.T) {
	// A little-endian byte-order mark, a lone high surrogate, "A", a lone low
	// surrogate, "B", a well-formed pair for U+1F600, and a newline.
	data := []byte{0xff, 0xfe, 0x00, 0xd8, 'A', 0, 0x00, 0xdc, 'B', 0, 0x3d, 0xd8, 0x00, 0xde, '\n', 0}
	r, ok := utf16Text(bufio.NewReader(bytes.NewReader(data)))
	if !ok {
		t.Fatal("the byte-order mark was not seen")
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	bad := string(utf8.RuneError)
	if want := bad + "A" + bad + "B" + string(rune(0x1f600)) + "\n"; string(got) != want {
		t.Errorf("read %q, want %q", got, want)
	}
}
