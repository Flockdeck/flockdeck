package baton

import (
	"regexp"
	"strings"
)

// ansiRe matches the terminal escape sequences that can be written into text a
// person later reads in a terminal: CSI sequences, OSC strings (a window title
// or a hyperlink) ended by BEL or ST, and a lone escape and the character after
// it.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[@-_]?`)

// CleanText removes terminal escape sequences and control characters from s,
// keeping newlines and tabs. A baton is plain text, and one written to a
// terminal by `baton show` or `baton list` must not be able to move the cursor,
// retitle the window or hide what it says.
func CleanText(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		// A line or paragraph separator is a line break to whatever draws it.
		if r == 0x2028 || r == 0x2029 {
			return '\n'
		}
		if stripped(r) {
			return -1
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return -1
		}
		return r
	}, s)
}

// stripped is the format characters CleanText takes out of text: the ones that
// hide or reorder what is written (zero width space, bidi controls and marks,
// the word joiner, the byte order mark, a soft hyphen). The zero width joiner and
// non-joiner (U+200D, U+200C) and the variation selectors are not among them:
// emoji sequences, Persian and Hindi are written with them. They are ignored
// when text is matched for secrets, and left in the text. See ignorable.
func stripped(r rune) bool {
	switch {
	case r == 0x200B, r == 0x200E, r == 0x200F, r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069, r == 0xFEFF, r == 0x00AD:
		return true
	}
	return false
}
