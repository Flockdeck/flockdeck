package record

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// invisible reports whether r is a character that shows nothing and can sit
// inside a secret without changing how it reads: a format character (zero-width
// space, joiner, word joiner, a bidi control, a soft hyphen), a variation
// selector, a combining grapheme joiner, or a filler.
func invisible(r rune) bool {
	if r < 0x80 {
		return false
	}
	switch {
	case unicode.Is(unicode.Cf, r):
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF, r >= 0x180B && r <= 0x180D:
		return true
	}
	switch r {
	case 0x034F, 0x115F, 0x1160, 0x3164, 0xFFA0:
		return true
	}
	return false
}

// withoutInvisible returns s with its invisible characters removed, and for each
// byte of the result where that byte is in s. It returns nil, nil when s has none.
func withoutInvisible(s string) (string, []int) {
	if strings.IndexFunc(s, invisible) < 0 {
		return "", nil
	}
	var b strings.Builder
	b.Grow(len(s))
	at := make([]int, 0, len(s))
	for k := 0; k < len(s); {
		r, n := utf8.DecodeRuneInString(s[k:])
		if !invisible(r) {
			b.WriteString(s[k : k+n])
			for j := 0; j < n; j++ {
				at = append(at, k+j)
			}
		}
		k += n
	}
	return b.String(), at
}

// spansIn runs find on s and on s without its invisible characters, and returns
// both sets as spans of s. A secret split by an invisible character is found in
// the second and covers the character inside it.
func spansIn(s string, find func(string) []Span) []Span {
	out := find(s)
	t, at := withoutInvisible(s)
	if at == nil {
		return out
	}
	for _, sp := range find(t) {
		if sp.End <= sp.Start {
			continue
		}
		out = append(out, Span{at[sp.Start], at[sp.End-1] + 1, sp.Kind})
	}
	return merge(out)
}
