package baton

import (
	"regexp"
	"strings"
)

// wrappedRuns finds a key that was wrapped over several lines by a fixed width, and
// marks every line of it. wrappedTokens looks at a line and the lines after it; this
// looks at the whole run, so that a line in the middle that looks like a name or a
// word on its own is still taken when it sits between key lines of the same width.
//
// A run is a first line whose last word (after any NAME= or "name: ") is key-like,
// then lines that are each one key-like word, all but the last as wide as the second
// (to within a character), the last no wider. Three lines or more are a run when the
// pieces joined look random, or start like a provider's key; two lines when the first
// is at least sixteen characters, which keeps two ordinary words apart. A column of
// full-length hashes, a column of names with underscores, and the body of a
// certificate are not runs.
func wrappedRuns(text string) []wrapped {
	lines := strings.Split(text, "\n")
	starts := make([]int, len(lines))
	off := 0
	for i, l := range lines {
		starts[i] = off
		off += len(l) + 1
	}
	var out []wrapped
	for i := 0; i+1 < len(lines); i++ {
		if i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "-----BEGIN") {
			continue
		}
		left, leftAt, _ := trailingRun(lines[i], starts[i])
		if left == "" {
			continue
		}
		key := left[strings.LastIndexAny(left, "=:")+1:]
		keyAt := leftAt + (len(left) - len(key))
		if len(key) < 8 || !keyLikeWord(key) {
			continue
		}
		pieces := []piece{{key, keyAt}}
		for j := i + 1; j < len(lines) && j <= i+maxRunLines; j++ {
			run, at, whole := leadingRun(lines[j], starts[j])
			if run == "" || !keyLikeWord(run) {
				break
			}
			pieces = append(pieces, piece{run, at})
			if !whole {
				break
			}
		}
		n := len(pieces)
		if n < 2 || !isWrapRun(pieces) {
			continue
		}
		for _, p := range pieces {
			out = append(out, wrapped{start: p.at, end: p.at + len(p.text), kind: "high-entropy"})
		}
		i += n - 2 // the lines it took are not the start of another run
	}
	return out
}

// uuidRe and wordHexRe are the shapes of identifiers that are listed in a column.
var (
	uuidRe    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	wordHexRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*[-_][0-9a-f]{8,}$`)
)

// qualifiesAsKey reports whether one line of a run reads like a key by itself: it starts
// with a provider's prefix, or is at least 20 characters of base64 or base62 with
// characters of more than one class and is not a name.
func qualifiesAsKey(w string) bool {
	for _, pre := range providerPrefixes {
		if strings.HasPrefix(w, pre) {
			return true
		}
	}
	return len(w) >= 20 && mixedClasses(w) && !identLike(w) && !nameLike(w)
}

// maxRunLines bounds how many lines of one run are read.
const maxRunLines = 64

// keyLikeWord is a word made of the characters a key is (base64 and its URL form, with
// the = that pads it).
func keyLikeWord(w string) bool {
	return b64only(strings.TrimRight(w, "="))
}

// providerPrefixes start the keys of providers whose keys are wrapped when they are
// pasted: a run that starts with one is a key whatever its randomness.
var providerPrefixes = []string{"ghp_", "gho_", "ghs_", "ghu_", "github_pat_", "glpat-", "sk-", "sk_live_", "sk_test_", "xoxb-", "xoxp-", "AKIA", "AIza", "npm_", "shpat_"}

func isWrapRun(p []piece) bool {
	n := len(p)
	var joined strings.Builder
	for _, x := range p {
		joined.WriteString(x.text)
	}
	j := joined.String()
	// A column of whole hashes, a column of names, are not a wrapped key.
	hex, names := 0, 0
	for _, x := range p {
		if isHex(x.text) && (len(x.text) == 32 || len(x.text) == 40 || len(x.text) == 64) {
			hex++
		}
		if strings.Contains(x.text, "_") && identLike(x.text) {
			names++
		}
	}
	if hex == n || names*2 >= n {
		return false
	}
	// A column of hex ids of any one length from 6 to 64 (container ids, abbreviated
	// commits), of UUIDs, or of names with a hex tail (user-3f9a...), is a list.
	allHex, listed := true, 0
	for _, x := range p {
		if !isHex(x.text) || len(x.text) < 6 || len(x.text) > 64 {
			allHex = false
		}
		if uuidRe.MatchString(x.text) || wordHexRe.MatchString(x.text) {
			listed++
		}
	}
	if allHex || listed*2 >= n {
		return false
	}
	// Somewhere in it is a line that reads like a key by itself.
	any := false
	for _, x := range p {
		if qualifiesAsKey(x.text) {
			any = true
		}
	}
	// Or, over three lines or more, the whole of it is long and of more than one class:
	// lines narrower than 20 characters are a key wrapped narrow.
	if !any && !(n >= 3 && len(j) >= 40 && mixedClasses(j) && !identLike(j)) {
		return false
	}
	prefixed := false
	for _, pre := range providerPrefixes {
		if strings.HasPrefix(p[0].text, pre) {
			prefixed = true
		}
	}
	if n == 2 {
		// The rest of a key has a digit in it or both cases; a plain word after a key (export) does not.
		return len(p[0].text) >= 16 && len(p[1].text) >= 6 && mixedClasses(p[1].text) && (prefixed || looksRandom(j))
	}
	// The width: the second line is as wide as the lines in the middle, to a
	// character, and the first and last are no wider.
	w := len(p[1].text)
	for k := 1; k < n-1; k++ {
		if d := len(p[k].text) - w; d < -1 || d > 1 {
			return false
		}
	}
	if w < 8 || len(p[0].text) > w+1 || len(p[n-1].text) > w+1 {
		return false
	}
	return prefixed || looksRandom(j) || len(j) >= 24 && mixedClasses(j) && !identLike(j)
}
