package record

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// invisible reports whether r is a character that shows nothing and can sit
// inside a secret without changing how it reads: a format character (zero-width
// space, joiner, word joiner, a bidi control, a soft hyphen, a tag), a variation
// selector, one of Unicode's other default-ignorable characters, a combining
// grapheme joiner, or a filler.
func invisible(r rune) bool {
	if r < 0x80 {
		return false
	}
	if unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Variation_Selector, r) ||
		unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) {
		return true
	}
	switch r {
	case 0x034F, 0x115F, 0x1160, 0x3164, 0xFFA0:
		return true
	}
	return false
}

// stripInvisible returns s with its invisible characters removed, and whether it
// had any. It builds nothing when it had none.
func stripInvisible(s string) (string, bool) {
	if strings.IndexFunc(s, invisible) < 0 {
		return s, false
	}
	var b strings.Builder
	b.Grow(len(s))
	for k := 0; k < len(s); {
		r, n := utf8.DecodeRuneInString(s[k:])
		if !invisible(r) {
			b.WriteString(s[k : k+n])
		}
		k += n
	}
	return b.String(), true
}

// cut is a run of invisible characters taken out of a text: where in the new text
// it was, and how many bytes have been taken out up to and including it.
type cut struct{ at, removed int }

// stripInvisibleCuts is stripInvisible for a caller that has to map offsets in
// the result back to the text. It keeps one entry for each run taken out, not
// one for each byte.
func stripInvisibleCuts(s string) (string, []cut) {
	if strings.IndexFunc(s, invisible) < 0 {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	var cuts []cut
	removed := 0
	for k := 0; k < len(s); {
		r, n := utf8.DecodeRuneInString(s[k:])
		if invisible(r) {
			removed += n
			if m := len(cuts); m > 0 && cuts[m-1].at == b.Len() {
				cuts[m-1].removed = removed
			} else {
				cuts = append(cuts, cut{b.Len(), removed})
			}
		} else {
			b.WriteString(s[k : k+n])
		}
		k += n
	}
	return b.String(), cuts
}

// orig is where the byte at offset x of the stripped text is in the text. Runs
// taken out just before it do not count: a span that starts at x starts after them.
func orig(cuts []cut, x int) int {
	i := sort.Search(len(cuts), func(i int) bool { return cuts[i].at > x })
	if i == 0 {
		return x
	}
	return x + cuts[i-1].removed
}

// spansIn runs find on s and on s without its invisible characters, and returns
// both sets as spans of s. A secret split by an invisible character is found in
// the second and covers the characters inside it.
func spansIn(s string, find func(string) []Span) []Span {
	out := find(s)
	t, cuts := stripInvisibleCuts(s)
	if cuts == nil {
		return out
	}
	for _, sp := range find(t) {
		if sp.End <= sp.Start {
			continue
		}
		out = append(out, Span{orig(cuts, sp.Start), orig(cuts, sp.End-1) + 1, sp.Kind})
	}
	return merge(out)
}
