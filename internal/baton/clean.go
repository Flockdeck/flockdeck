package baton

import "regexp"

// ansiRe matches the terminal escape sequences that can be written into text a
// person later reads in a terminal: CSI sequences, OSC strings (a window title
// or a hyperlink) ended by BEL or ST, and a lone escape and the character after
// it.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[@-_]?`)

// CleanText removes terminal escape sequences and control characters from s,
// keeping newlines and tabs. A baton is plain text, and one written to a
// terminal by `baton show` or `baton list` must not be able to move the cursor,
// retitle the window or hide what it says. It also removes the characters that
// draw nothing and so can carry text a reader does not see: tag characters,
// the variation selector supplement, the Hangul and Khmer fillers, and the
// joiners and selectors that sit next to an ASCII character (see stripped and
// joinerStripped).
func CleanText(s string) string {
	// One pass can leave text the next would change: a joiner that had a
	// zero width space on one side was kept, and has an ASCII letter beside it
	// once the space is gone. So it goes on until nothing changes.
	for i := 0; i < maxCleanPasses; i++ {
		t := cleanOnce(s)
		if t == s {
			break
		}
		s = t
	}
	return s
}

// maxCleanPasses bounds the passes of CleanText. Each pass that changes anything
// makes the text shorter, and what one pass leaves for the next is a joiner run
// that a removed character had been guarding, so a few passes settle any text
// that is not built to take as many.
const maxCleanPasses = 4

// maxJoinerRun is the most joiners and variation selectors kept in a row. A
// legitimate sequence has two at most between characters (an emoji, U+FE0F, then
// U+200D), and a longer run is a place to hide bits.
const maxJoinerRun = 2

// cleanOnce is one pass of CleanText.
func cleanOnce(s string) string {
	s = ansiRe.ReplaceAllString(s, "")
	if isPlainClean(s) {
		return s
	}
	rs := []rune(s)
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '\n' || r == '\t' {
			out = append(out, r)
			continue
		}
		// A line or paragraph separator is a line break to whatever draws it.
		if r == 0x2028 || r == 0x2029 {
			out = append(out, '\n')
			continue
		}
		if stripped(r) {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			continue
		}
		if joiner(r) {
			// A run of them is judged as one, by what is on each side of it.
			k := i
			for k < len(rs) && joiner(rs[k]) {
				k++
			}
			var prev, next rune = -1, -1
			if i > 0 {
				prev = rs[i-1]
			}
			if k < len(rs) {
				next = rs[k]
			}
			if !joinerStripped(r, prev, next) {
				out = append(out, rs[i:min(k, i+maxJoinerRun)]...)
			}
			i = k - 1
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// isPlainClean reports a string with nothing for CleanText to do: printable
// ASCII, newlines and tabs.
func isPlainClean(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x80 || c == 0x7f || (c < 0x20 && c != '\n' && c != '\t') {
			return false
		}
	}
	return true
}

// stripped is the format characters CleanText takes out of text: the ones that
// hide or reorder what is written (zero width space, bidi controls and marks,
// the word joiner and the invisible operators, the byte order mark, a soft
// hyphen), the ones that draw nothing and have no use in ordinary text (the
// Hangul and Khmer fillers, the Mongolian free variation selectors, the
// deprecated format controls), and the tag characters (U+E0000 to U+E007F) and
// variation selector supplement (U+E0100 to U+E01EF), which a model reads as
// ASCII and a person sees as nothing. See also joinerStripped.
func stripped(r rune) bool {
	switch {
	case r == 0x200B, r == 0x200E, r == 0x200F, r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x206F, r == 0xFEFF, r == 0x00AD, r == 0x061C,
		r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0, r == 0x17B4, r == 0x17B5,
		r >= 0x180B && r <= 0x180F, r >= 0xE0000 && r <= 0xE007F, r >= 0xE0100 && r <= 0xE01EF:
		return true
	}
	return false
}

// joiner reports the characters that are kept in text written in the scripts
// that need them and taken out where they only hide something.
func joiner(r rune) bool {
	return r == 0x200C || r == 0x200D || (r >= 0xFE00 && r <= 0xFE0F)
}

// joinerStripped reports whether a zero width joiner or non-joiner or a
// variation selector is taken out. Emoji sequences, Persian and Hindi are
// written with them, between characters that are not ASCII, so those are kept.
// Next to an ASCII letter, digit or punctuation mark they join nothing, and are
// the way a string is written in a form that looks the same and matches
// nothing (or is read as bits). The one use next to ASCII is a keycap
// ("1" U+FE0F U+20E3), which is kept.
func joinerStripped(r, prev, next rune) bool {
	if r >= 0xFE00 && r <= 0xFE0F && next == 0x20E3 {
		return false
	}
	ascii := func(x rune) bool { return x >= 0 && x < 0x80 }
	if r >= 0xFE00 && r <= 0xFE0F {
		// A selector goes after the character it changes, which is what is
		// before it; what follows is not its business (an emoji, then a space).
		return prev < 0 || ascii(prev)
	}
	if (r == 0x200C || r == 0x200D) && (prev < 0 || next < 0) {
		return true
	}
	return ascii(prev) || ascii(next)
}
