package bridge

import (
	"strings"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/session/transcript"
)

// clean makes a string from the agent safe to hand to a UI or a terminal. It
// removes ESC sequences (CSI, OSC and the other string kinds, and two-byte
// escapes), C0 controls except newline and tab, carriage return, DEL, C1
// controls, line and paragraph separators, and the bidi marks and overrides
// that reorder text. Invalid UTF-8 becomes U+FFFD.
//
// An escape sequence split across two chunks of a stream is not recognised:
// its ESC is removed and the rest is left as visible text.
func clean(s string) string {
	if plainUTF8(s) {
		return s
	}
	s = strings.ToValidUTF8(s, "�")
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b:
			i = skipEscape(s, i)
		case c < 0x80:
			if c == '\n' || c == '\t' || (c >= 0x20 && c != 0x7f) {
				b.WriteByte(c)
			}
			i++
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			i += n
			if !dropRune(r) {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// plainUTF8 reports whether s is valid UTF-8 with nothing clean would drop.
func plainUTF8(s string) bool {
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			if c == 0x7f || (c < 0x20 && c != '\n' && c != '\t') {
				return false
			}
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 || dropRune(r) {
			return false
		}
		i += n
	}
	return true
}

func dropRune(r rune) bool {
	switch {
	case r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200b, r == 0x200c, r == 0x200d, r == 0x200e, r == 0x200f, r == 0x2028, r == 0x2029, r == 0x2060, r == 0xfeff:
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0000 && r <= 0xe01ef: // variation selectors, tag characters
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	case r >= 0x206a && r <= 0x206f, r >= 0xfff9 && r <= 0xfffc: // deprecated format characters, interlinear annotation and object replacement
		return true
	case r == 0x00ad, r == 0x034f, r == 0x180e, r == 0x115f, r == 0x3164, r == 0x2800: // soft hyphen, joiner, fillers, blank braille
		return true
	}
	return false
}

// skipEscape returns the index after the escape sequence that starts at i.
func skipEscape(s string, i int) int {
	i++
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[': // CSI: parameters, intermediates, one final byte
		i++
		for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
			i++
		}
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
			i++
		}
		return i
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: up to BEL or ST
		for i++; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return i
	}
	// Any other escape: intermediates, then one final byte.
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] < 0x80 { // not the lead byte of a multibyte rune
		i++
	}
	return i
}

// Line is line for another package: the one-line, cleaned and cut text a card
// shows for a label. An adapter that gives a label back to its agent gives this,
// so that what the person chose is what the agent is told.
func Line(s string, max int) string { return line(s, max) }

// line is the first line of a cleaned s, cut to max runes: for labels, names,
// ids and paths, which must not carry controls or span lines.
func line(s string, max int) string {
	return transcript.OneLine(clean(capBytes(s, max*4+16)), max)
}

// capBytes cuts s to at most max bytes without splitting a rune.
func capBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}
