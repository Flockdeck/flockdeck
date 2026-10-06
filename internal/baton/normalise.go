package baton

import "unicode/utf8"

// ignorable reports whether r draws nothing, or nothing a pattern should see:
// zero width spaces, joiners and non-joiners, bidi controls and marks, the word
// joiner and the invisible operators, variation selectors, tag characters, the
// Hangul and Braille blanks, the byte order mark, a soft hyphen. Put in the
// middle of a key they stop a pattern matching it while leaving it looking
// whole. They are left out when text is matched, and stay in the text.
func ignorable(r rune) bool {
	switch {
	case r == 0x034F, r == 0x061C, r == 0x115F, r == 0x1160, r == 0x17B4, r == 0x17B5,
		r >= 0x180B && r <= 0x180F, r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E,
		r >= 0x2060 && r <= 0x206F, r == 0x2800, r == 0x3164, r >= 0xFE00 && r <= 0xFE0F,
		r == 0xFEFF, r == 0xFFA0, r >= 0xE0000 && r <= 0xE0FFF, r == 0x00AD:
		return true
	}
	return false
}

// spaceLike reports a space that is not U+0020 but separates words the same way:
// no-break, figure and narrow no-break, and ideographic.
func spaceLike(r rune) bool {
	return r == 0x00A0 || r == 0x2007 || r == 0x202F || r == 0x3000
}

// normalise returns s as it is matched: ignorable characters removed, and
// full-width ASCII letters, digits and punctuation (U+FF01 to U+FF5E) folded to
// ASCII, so a key typed with them is still a key. The odd spaces (no-break,
// ideographic) are the one thing text can be read two ways about: inside a token
// they may be put in to break it, and between words they are spaces. spaces says
// which way: false drops them, so they cannot break a token, and true makes them
// ordinary spaces. A scrub looks at both and takes what either finds. idx maps
// each byte of the result, and its end, back to where it came from in s, so a
// span found in the result can be taken out of the original. When nothing
// changes it returns s and a nil idx.
func normalise(s string, spaces bool) (string, []int) {
	plain := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			plain = false
			break
		}
	}
	if plain {
		return s, nil
	}
	out := make([]byte, 0, len(s))
	idx := make([]int, 0, len(s)+1)
	changed := false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case ignorable(r):
			changed = true
		case spaceLike(r):
			if spaces {
				out = append(out, ' ')
				idx = append(idx, i)
			}
			changed = true
		case r >= 0xFF01 && r <= 0xFF5E:
			out = append(out, byte(r-0xFEE0))
			idx = append(idx, i)
			changed = true
		default:
			for k := 0; k < size; k++ {
				out = append(out, s[i+k])
				idx = append(idx, i)
			}
		}
		i += size
	}
	if !changed {
		return s, nil
	}
	idx = append(idx, len(s))
	return string(out), idx
}
