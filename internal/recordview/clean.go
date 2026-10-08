package recordview

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/record"
)

// A string that comes from a file is made safe to show and to look for secrets in
// in one step, cleanText, and redacted in a second, redactCleaned.
//
// Cleaning replaces invalid UTF-8 with U+FFFD, removes control characters other
// than a line feed and a tab, the C1 range, terminal escape sequences whole (CSI,
// OSC, DCS, SOS, PM and APC strings and the two-byte escapes, in their 7-bit and
// C1 forms), and every character that has no width or only changes how its
// neighbours are drawn: the format characters of Unicode (zero-width space,
// joiner and non-joiner, word joiner, soft hyphen, byte order mark,
// bidirectional marks and overrides, the invisible operators, tag characters),
// variation selectors and the fillers that draw nothing. U+2028 and U+2029 become
// a line feed.
//
// The client renders with textContent, so this is not what stops markup. It stops
// an escape or a reordering mark from misleading a reader, and it stops
// characters that are invisible from splitting a secret so that no pattern
// matches it.

// cleaned is a string after cleanText.
type cleaned struct {
	// text is the string to show.
	text string
	// alts are other readings of the same string, for text that had escape
	// sequences in it. A terminal takes the byte after a lone ESC, or the last byte
	// of a sequence, for part of the sequence and does not show it; a secret that
	// follows one may start with that byte or be broken by a sequence that was
	// meant as something else. So the string is also read with the short forms left
	// as text (only a sequence with all its parts is removed), and with every
	// sequence left as text. A secret found in any reading is replaced in text.
	alts []view
}

// view is a text to look for secrets in and where its bytes are in the text that
// is shown: at[k] is the offset in that text of byte k of this one, or -1; nil
// means they are the same text.
type view struct {
	text string
	at   []int32
}

// stripUnsafe is the text of cleanText.
func stripUnsafe(s string) string { return cleanText(s).text }

func cleanText(s string) cleaned {
	if !strings.ContainsRune(s, 0x1b) && !strings.ContainsAny(s, "\u0090\u0098\u009b\u009d\u009e\u009f") {
		return cleaned{text: loose(s)}
	}
	whole, short := sequences(s, false), sequences(s, true)
	if len(whole) == 0 {
		return cleaned{text: loose(s)}
	}
	var raw []byte
	var origin []int32
	for i := 0; i < len(s); {
		n, keep, repl := step(s, i)
		switch {
		case repl != "":
			for k := 0; k < len(repl); k++ {
				raw, origin = append(raw, repl[k]), append(origin, int32(i))
			}
		case keep:
			for k := 0; k < n; k++ {
				raw, origin = append(raw, s[i+k]), append(origin, int32(i))
			}
		}
		i += n
	}
	text, src := drop(raw, origin, whole)
	inText := make([]int32, len(raw))
	for k := range inText {
		inText[k] = -1
	}
	for j, k := range src {
		inText[k] = int32(j)
	}
	c := cleaned{text: string(text), alts: []view{{string(raw), inText}}}
	if len(short) != len(whole) {
		t, src := drop(raw, origin, short)
		at := make([]int32, len(src))
		for j, k := range src {
			at[j] = inText[k]
		}
		c.alts = append(c.alts, view{string(t), at})
	}
	return c
}

// drop returns raw without the bytes whose origin is in rngs, and where each byte
// of the result was in raw.
func drop(raw []byte, origin []int32, rngs [][2]int) (out []byte, src []int32) {
	out = make([]byte, 0, len(raw))
	src = make([]int32, 0, len(raw))
	ri := 0
	for k := range raw {
		o := int(origin[k])
		for ri < len(rngs) && rngs[ri][1] <= o {
			ri++
		}
		if ri < len(rngs) && o >= rngs[ri][0] {
			continue
		}
		out, src = append(out, raw[k]), append(src, int32(k))
	}
	return out, src
}

// loose is s with everything cleanText removes except escape sequences, of which
// only the control bytes are removed.
func loose(s string) string {
	var out []byte // nil until something is removed
	last := 0      // s[last:i] is kept and not yet copied to out
	for i := 0; i < len(s); {
		n, keep, repl := step(s, i)
		if !keep || repl != "" {
			if out == nil {
				out = make([]byte, 0, len(s))
			}
			out = append(out, s[last:i]...)
			out = append(out, repl...)
			last = i + n
		}
		i += n
	}
	if out == nil {
		return s
	}
	return string(append(out, s[last:]...))
}

// step looks at the character at s[i]. It returns the bytes the character takes,
// whether it stays as it is, and, if it does not, what takes its place.
func step(s string, i int) (n int, keep bool, repl string) {
	if c := s[i]; c < utf8.RuneSelf {
		return 1, c == '\n' || c == '\t' || c >= 0x20 && c < 0x7f, ""
	}
	r, n := utf8.DecodeRuneInString(s[i:])
	switch {
	case r == utf8.RuneError && n == 1:
		return 1, false, "�"
	case r == 0x2028 || r == 0x2029:
		return n, false, "\n"
	case invisible(r):
		return n, false, ""
	}
	return n, true, ""
}

// sequences returns the byte ranges of s that are escape sequences of more than
// their introducer. With short unset it includes the two-byte forms, ESC and one
// byte, and the strings and control sequences cut short; with it set it has only
// sequences that are whole.
func sequences(s string, short bool) [][2]int {
	var out [][2]int
	fail := [2]int{-1, -1}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == 0x1b:
			if j := skipEscape(s, i, &fail, short); j > i+1 {
				out = append(out, [2]int{i, j})
				i = j - 1
			}
		case c == 0xc2 && i+1 < len(s):
			j := 0
			switch s[i+1] {
			case 0x9b:
				j = skipCSI(s, i+2, short)
			case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
				j = skipString(s, i+2, s[i+1] == 0x9d, &fail)
			}
			if j > i+2 {
				out = append(out, [2]int{i, j})
				i = j - 1
			}
		}
	}
	return out
}

// skipEscape returns the offset after the escape sequence that starts with the
// ESC at s[i], or i+1 if it is not one. A sequence that is cut short ends where
// its valid part does, so that the rest of the text is shown as it is. With strict
// set, only a sequence with all its parts counts.
func skipEscape(s string, i int, fail *[2]int, strict bool) int {
	if i+1 >= len(s) {
		return i + 1
	}
	switch c := s[i+1]; {
	case c == '[':
		return skipCSI(s, i+2, strict)
	case c == ']':
		return wholeString(s, i, true, fail, strict)
	case c == 'P' || c == 'X' || c == '^' || c == '_':
		return wholeString(s, i, false, fail, strict)
	case c >= 0x20 && c <= 0x2f:
		// ESC, intermediate bytes, a final byte (ESC ( B selects a character set).
		j := i + 1
		for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
			j++
		}
		if j < len(s) && s[j] >= 0x30 && s[j] <= 0x7e && (!strict || j == i+2 && strings.IndexByte(escapeFinals[c], s[j]) >= 0) {
			return j + 1
		}
		if strict {
			return i + 1
		}
		return j
	case c >= 0x30 && c <= 0x7e:
		if strict && c != 0x37 && c != 0x38 && c != 0x3d && c != 0x3e {
			return i + 1
		}
		return i + 2
	}
	return i + 1
}

// skipCSI returns the offset after the parameter, intermediate and final bytes
// of a control sequence whose introducer ends at from. With strict set, a
// sequence without its final byte counts for nothing.
func skipCSI(s string, from int, strict bool) int {
	j := from
	for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
		j++
	}
	if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
		return j + 1
	}
	if strict {
		return from - 2
	}
	return j
}

// skipString returns the offset after a string sequence (OSC, DCS, SOS, PM or
// APC) whose introducer ends at from: past its terminator, which is ESC \, the
// C1 string terminator, or for an OSC also BEL. One with no terminator on its
// line is taken for no more than its introducer, and what follows is text: a
// terminal would take the rest of the line for the string, but a line cut short
// is more likely than a string that is meant to hide the rest of it. fail is the
// offset of a line end up to which a look has found no terminator, so that a line
// of many introducers is read once.
func skipString(s string, from int, bel bool, fail *[2]int) int {
	k := 0
	if bel {
		k = 1
	}
	if from <= fail[k] {
		return from
	}
	for j := from; j < len(s); j++ {
		switch c := s[j]; {
		case c == '\n':
			fail[k] = j
			return from
		case c == 0x07 && bel:
			return j + 1
		case c == 0x1b && j+1 < len(s) && s[j+1] == '\\':
			return j + 2
		case c == 0xc2 && j+1 < len(s) && s[j+1] == 0x9c:
			return j + 2
		}
	}
	fail[k] = len(s)
	return from
}

// invisible reports whether r is removed for having no width, drawing nothing or
// changing how the text around it is drawn.
func invisible(r rune) bool {
	switch {
	case r >= 0x7f && r <= 0x9f:
		return true
	case unicode.Is(unicode.Cf, r):
		// Soft hyphen, Arabic format marks, Mongolian vowel separator, the zero-width
		// characters U+200B to U+200F, the bidirectional controls U+202A to U+202E and
		// U+2066 to U+2069, the word joiner and invisible operators U+2060 to U+2064,
		// the deprecated U+206A to U+206F, byte order mark, interlinear annotation
		// marks, tag characters.
		return true
	case r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef, r >= 0x180b && r <= 0x180d:
		// Variation selectors and Mongolian free variation selectors.
		return true
	case r == 0x034f, r == 0x115f, r == 0x1160, r == 0x17b4, r == 0x17b5, r == 0x3164, r == 0xffa0:
		// Combining grapheme joiner and the fillers that draw nothing.
		return true
	}
	return false
}

// gapRune reports whether r is a space that is not the plain one: a tab and the
// spaces of other widths. They are shown as they are, but a secret with one in
// the middle of it is still a secret.
func gapRune(r rune) bool {
	return r == '\t' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x202f || r == 0x205f || r == 0x3000
}

// withoutGaps is v with its gap characters taken out.
func (v view) withoutGaps() view {
	if !strings.ContainsFunc(v.text, gapRune) {
		return v
	}
	out := make([]byte, 0, len(v.text))
	at := make([]int32, 0, len(v.text))
	for i := 0; i < len(v.text); {
		r, w := utf8.DecodeRuneInString(v.text[i:])
		if !gapRune(r) {
			for k := 0; k < w; k++ {
				out = append(out, v.text[i+k])
				if v.at == nil {
					at = append(at, int32(i+k))
				} else {
					at = append(at, v.at[i+k])
				}
			}
		}
		i += w
	}
	return view{string(out), at}
}

// ranges returns where in the text that is shown the secrets that record finds in
// the view are, as byte ranges.
func (v view) ranges() [][2]int {
	var out [][2]int
	for _, sp := range record.RedactSpans(v.text) {
		if v.at == nil {
			out = append(out, [2]int{sp.Start, sp.End})
			continue
		}
		from, to := -1, -1
		for k := sp.Start; k < sp.End; k++ {
			if v.at[k] >= 0 {
				if from < 0 {
					from = int(v.at[k])
				}
				to = int(v.at[k]) + 1
			}
		}
		if from >= 0 {
			out = append(out, [2]int{from, to})
		}
	}
	return out
}

// replaceRanges replaces the byte ranges of s, which may overlap, with the mark
// record puts where it has taken something out.
func replaceRanges(s string, rs [][2]int) string {
	sort.Slice(rs, func(i, j int) bool { return rs[i][0] < rs[j][0] })
	var b strings.Builder
	b.Grow(len(s))
	done := 0
	for _, r := range rs {
		if r[0] < done {
			done = max(done, r[1])
			continue
		}
		b.WriteString(s[done:r[0]])
		b.WriteString(record.Redacted)
		done = r[1]
	}
	b.WriteString(s[done:])
	return b.String()
}

// redactCleaned redacts a cleaned string.
//
// A string with nothing in it that hides a secret is redacted by record.Redact as
// it is. If it has tabs or the spaces of other widths, it is also looked at
// without them; if it had escape sequences, it is also looked at in its other
// readings. What is found in each of these views is replaced in the text, all of
// it together, so that a secret broken in two by a tab is replaced from end to end
// and not in the part a first look could see. The tabs stay in the text.
// Replacing a secret that has a tab inside it takes the tab with it. One that is
// followed by a tab and a word of the characters a secret is made of takes the
// word as well, as a pattern cannot tell it from the rest of the secret.
func redactCleaned(c cleaned) string {
	if len(c.alts) == 0 && !strings.ContainsFunc(c.text, gapRune) {
		return record.Redact(c.text)
	}
	return round(c.text, c.alts)
}

// round looks at text in its views, replaces what is found and runs record.Redact
// on the result.
func round(text string, alts []view) string {
	views := []view{{text: text}}
	if v := views[0].withoutGaps(); v.at != nil {
		views = append(views, v)
	}
	for _, a := range alts {
		views = append(views, a)
		if v := a.withoutGaps(); len(v.text) != len(a.text) {
			views = append(views, v)
		}
	}
	var rs [][2]int
	for _, v := range views {
		rs = append(rs, v.ranges()...)
	}
	if len(rs) > 0 {
		text = replaceRanges(text, rs)
	}
	return record.Redact(text)
}

// redactText is redactCleaned for a string that has no sequences in it.
func redactText(s string) string { return redactCleaned(cleaned{text: s}) }

// wholeString is skipString for the string sequence introduced by the ESC at
// s[i]. With strict set, one with no terminator counts for nothing.
func wholeString(s string, i int, bel bool, fail *[2]int, strict bool) int {
	j := skipString(s, i+2, bel, fail)
	if strict && j == i+2 {
		return i + 1
	}
	return j
}

// escapeFinals are the final bytes of the sequences of ESC, one intermediate byte
// and a final byte that a terminal acts on, by intermediate byte: ESC ( B selects
// a character set, ESC # 8 fills the screen.
var escapeFinals = map[byte]string{
	' ': "FGLMN", '#': "345678", '%': "@G", '(': "012AB", ')': "012AB", '*': "012AB", '+': "012AB",
}
