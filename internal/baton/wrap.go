package baton

import (
	"regexp"
	"strings"

	"github.com/jmwri/flockdeck/internal/record"
)

// maxWrapLines is how many lines a token is followed over. A terminal wraps a
// long key onto the next line or two, not onto a dozen.
const maxWrapLines = 4

// piece is one run of a wrapped token and where it starts in the text.
type piece struct {
	text string
	at   int
}

type wrapped struct {
	start, end int
	kind       string
}

// wrappedTokens finds provider-shaped tokens that a line break splits in two.
//
// For each line it takes the run of non-space text at the end of the line and
// the run at the start of the next, and looks for a token in the two joined
// that starts in the first and ends in the second. Joining the whole text
// instead would glue a token that ends a line to the first word of the next,
// and take the word with it, so a continuation is accepted only when the
// first run is not a whole token on its own, or the line ends in a backslash,
// or the second run itself looks like part of a token.
func wrappedTokens(text string) []wrapped {
	lines := strings.Split(text, "\n")
	starts := make([]int, len(lines))
	off := 0
	for i, l := range lines {
		starts[i] = off
		off += len(l) + 1
	}
	var out []wrapped
	skip := 0
	for i := 0; i+1 < len(lines); i++ {
		if i < skip {
			continue
		}
		left, leftAt, cont := trailingRun(lines[i], starts[i])
		if left == "" {
			continue
		}
		pieces := []piece{{left, leftAt}}
		for j := i + 1; j < len(lines) && j <= i+maxWrapLines; j++ {
			run, at, whole := leadingRun(lines[j], starts[j])
			if run == "" {
				break
			}
			pieces = append(pieces, piece{run, at})
			if !whole {
				break
			}
		}
		if len(pieces) < 2 {
			continue
		}
		var joined strings.Builder
		for _, p := range pieces {
			joined.WriteString(p.text)
		}
		// Provider-shaped tokens, and long runs that look random, that start in
		// the first run and end in a later one.
		type cand struct {
			start, end int
			kind       string
		}
		var cands []cand
		alone := map[int]bool{}
		for _, sp := range record.FindTokens(joined.String()) {
			cands = append(cands, cand{sp.Start, sp.End, sp.Kind})
		}
		for _, sp := range record.FindTokens(left) {
			alone[sp.Start] = true
		}
		// A random run wrapped over lines: only when the end of the first line
		// and the start of each next are made of the characters a key is, so that
		// a word at the end of a line is not joined to whatever begins the next.
		// What comes after a = or : on the first line (NAME=value) is the part
		// that continues.
		keyPart := left[strings.LastIndexAny(left, "=:")+1:]
		// A key that is wrapped has a long first line: the part before the break is
		// not a few characters (0x0002) at the end of a line of code.
		if keyLike, rest := b64only(keyPart), ""; keyLike && len(keyPart) >= 10 && (len(keyPart) < 24 || looksRandom(keyPart)) && fragmentLike(keyPart) {
			// The shortest run of following lines that makes a random run with
			// the first is the key; the lines after it are not part of it.
			end := len(left)
			for n, p := range pieces[1:] {
				// A next line that is a name (_SignatureScheme_name_4) is code, and not the
				// rest of a key.
				if !b64only(p.text) || strings.Contains(p.text, "_") && identLike(p.text) {
					break
				}
				rest += p.text
				end += len(p.text)
				if frag := keyPart + rest; fragmentLike(rest) && looksRandom(frag) {
					cands = append(cands, cand{len(left) - len(keyPart), end, "high-entropy"})
					// The lines it took are not the start of another run.
					skip = i + n + 2
					if looksRandom(keyPart) {
						alone[len(left)-len(keyPart)] = true
					}
					break
				}
			}
		}
		for _, c := range cands {
			if c.start >= len(left) || c.end <= len(left) {
				continue
			}
			if alone[c.start] && !cont && !tokenLike(pieces[1].text) {
				continue
			}
			// One mark for each line's part of the token, so that the line ends and the
			// indent between them stay where they were: a mark that took a newline
			// joined two lines of the text.
			base := 0
			for _, p := range pieces {
				a, b := max(c.start, base), min(c.end, base+len(p.text))
				if a < b {
					out = append(out, wrapped{start: p.at + (a - base), end: p.at + (b - base), kind: c.kind})
				}
				base += len(p.text)
			}
		}
	}
	return out
}

// unjoin maps an offset in the joined runs back to the text. An end offset
// that falls on the edge of a run belongs to that run, not the next.
func unjoin(ps []piece, o int, end bool) int {
	for _, p := range ps {
		if o < len(p.text) || (end && o == len(p.text)) {
			return p.at + o
		}
		o -= len(p.text)
	}
	last := ps[len(ps)-1]
	return last.at + len(last.text)
}

// bulletRe is the marker that starts a list item: a dash, a star, a quote
// mark, or a number.
var bulletRe = regexp.MustCompile(`^(?:[-*+>]|[0-9]+[.)])[ \t]+`)

// trailingRun is the non-space text at the end of a line, with a trailing
// backslash taken off and reported as a continuation, and any closing quote or
// comma after the text left off.
func trailingRun(line string, lineAt int) (run string, at int, continued bool) {
	line = strings.TrimRight(line, " \t\r")
	if rest, ok := strings.CutSuffix(line, `\`); ok {
		line, continued = rest, true
	}
	line = strings.TrimRight(line, "\"',;`")
	i := strings.LastIndexAny(line, " \t") + 1
	// An opening quote belongs to the text around the run, not to it.
	for i < len(line) && strings.IndexByte("\"'`", line[i]) >= 0 {
		i++
	}
	return line[i:], lineAt + i, continued
}

// leadingRun is the non-space text at the start of a line, past its indent, a
// list marker and an opening quote, and whether that run is all of the line, so
// a token can go on past it. A run that ends in a closing quote or a comma is
// the end of the token.
func leadingRun(line string, lineAt int) (run string, at int, whole bool) {
	trimmed := strings.TrimLeft(line, " \t")
	indent := len(line) - len(trimmed)
	trimmed = strings.TrimRight(trimmed, "\r")
	if loc := bulletRe.FindStringIndex(trimmed); loc != nil {
		trimmed, indent = trimmed[loc[1]:], indent+loc[1]
	}
	rest := strings.TrimLeft(trimmed, "\"'`")
	indent += len(trimmed) - len(rest)
	trimmed = rest
	j := strings.IndexAny(trimmed, " \t")
	if j < 0 {
		run, whole = strings.TrimSuffix(trimmed, `\`), true
	} else {
		run = trimmed[:j]
	}
	if t := strings.TrimRight(run, "\"',;`"); t != run {
		run, whole = t, false
	}
	return run, lineAt + indent, whole
}

// b64only reports whether s is made only of the characters a base64 or URL-safe
// key is, and is not empty.
func b64only(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '_' || c == '-') {
			return false
		}
	}
	return s != ""
}

// fragmentLike says a stretch of text could be a piece of a key: it has a digit,
// or switches from lower case to upper case at least twice. A word does not.
func fragmentLike(s string) bool {
	if len(s) < 6 {
		return false
	}
	flips := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			return true
		}
		if i > 0 && s[i-1] >= 'a' && s[i-1] <= 'z' && c >= 'A' && c <= 'Z' {
			flips++
		}
	}
	return flips >= 2
}

// tokenLike says a run is the sort of text a token is made of rather than a
// word: it has a digit, or switches from lower case to upper case more than
// once, is long enough to matter, and has none of = : / \\ that a setting or a
// path has.
func tokenLike(run string) bool {
	if len(run) < 8 || strings.ContainsAny(run, "=:/\\") {
		return false
	}
	var digit bool
	flips := 0
	for i := 0; i < len(run); i++ {
		c := run[i]
		if c >= '0' && c <= '9' {
			digit = true
		}
		if i > 0 && run[i-1] >= 'a' && run[i-1] <= 'z' && c >= 'A' && c <= 'Z' {
			flips++
		}
	}
	return digit || flips >= 2
}
