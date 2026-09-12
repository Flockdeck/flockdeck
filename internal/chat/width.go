package chat

import "unicode"

// displayWidth is how many columns s takes in a terminal, which is not how
// many characters it has: a Chinese, Japanese or Korean character, and most
// emoji, take two, and a combining mark takes none. Counted as one each, a
// line with them in it was drawn wider than the pane, and the terminal broke
// it a second time into a line and a fragment.
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

// runeWidth is the columns one character takes.
func runeWidth(r rune) int {
	switch {
	case r == 0x200d || (r >= 0xfe00 && r <= 0xfe0f) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r):
		// A joiner, a variation selector, a combining mark: drawn on the
		// character before it.
		return 0
	case isWide(r):
		return 2
	}
	return 1
}

// wideRanges are the blocks a terminal draws two columns wide: the East Asian
// wide and fullwidth scripts, and the emoji blocks. A block of mixed widths --
// the dingbats, the miscellaneous symbols -- is left at one, since counting a
// narrow character as two wraps a line early, which is the harmless mistake.
var wideRanges = [][2]rune{
	{0x1100, 0x115f},   // Hangul Jamo initials
	{0x2e80, 0x303e},   // CJK radicals, Kangxi, CJK symbols and punctuation
	{0x3041, 0x33ff},   // Hiragana, Katakana, Bopomofo, CJK compatibility
	{0x3400, 0x4dbf},   // CJK extension A
	{0x4e00, 0x9fff},   // CJK unified ideographs
	{0xa000, 0xa4cf},   // Yi
	{0xac00, 0xd7a3},   // Hangul syllables
	{0xf900, 0xfaff},   // CJK compatibility ideographs
	{0xfe30, 0xfe4f},   // CJK compatibility forms
	{0xff00, 0xff60},   // fullwidth forms
	{0xffe0, 0xffe6},   // fullwidth signs
	{0x1f300, 0x1f64f}, // pictographs, emoticons
	{0x1f680, 0x1f6ff}, // transport and map symbols
	{0x1f900, 0x1f9ff}, // supplemental symbols and pictographs
	{0x20000, 0x2fffd}, // CJK extensions B onwards
	{0x30000, 0x3fffd},
}

func isWide(r rune) bool {
	if r < 0x1100 {
		return false
	}
	for _, w := range wideRanges {
		if r >= w[0] && r <= w[1] {
			return true
		}
	}
	return false
}
