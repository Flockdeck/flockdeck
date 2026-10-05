package baton

import (
	"math"
	"regexp"
	"strings"
)

// This file is the scrubber's third layer: runs of text that look random. It is
// the layer with the most false positives to avoid, because a baton is full of
// paths, URLs, import paths, branch names and commit ids that are long and
// mixed-case and are not secrets. What it takes is decided a piece at a time, so
// that a URL loses only the piece of it that looks like a key, never its host.

// wordRe finds the runs worth a look: no space, no quote, no bracket. A word is
// ASCII: a run of any other character ends it, so a secret with accented or CJK
// letters next to it is not taken together with them.
var wordRe = regexp.MustCompile("[^\\s\"'<>(){}\\[\\],;`|\\x{80}-\\x{10FFFF}]{24,}")

// urlRe is the start of a URL.
var urlRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://`)

// pathRoot is the start of an absolute path on a system Flockdeck runs on.
var pathRoot = regexp.MustCompile(`^/(?:usr|etc|home|var|tmp|opt|srv|mnt|bin|sbin|lib|lib64|proc|dev|root|sys|run|Users|private|Library|Applications|Volumes)/`)

// nameWord is a word as it is written in a name: lower case, Capitalised,
// camelCase or UPPER, with digits only at the end.
var nameWord = regexp.MustCompile(`^(?:[a-z]+|(?:[A-Z][a-z]+)+[A-Z]*|[a-z]+(?:[A-Z][a-z]+)+[A-Z]*|[A-Z]+)?[0-9]*$`)

// pathPartMin is the shortest part of a path taken for a secret.
const pathPartMin = 24

func isLower(c byte) bool { return c >= 'a' && c <= 'z' }
func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

// isNameWord reports whether w is a word as it is written in a name: nameWord, or
// camelCase with acronyms in it (getHTTPResponseBodyAsString,
// ParseHTTPSRequestAndValidateInput), which nameWord's pattern cannot say.
func isNameWord(w string) bool {
	return nameWord.MatchString(w) || acronymWord(w)
}

// acronymWord reports whether w, ignoring digits at its end, reads as a run of
// words in camelCase with at least one acronym of two letters or more in it. Each
// word has to be a Capital and lower case letters, or an acronym, the first word
// may be lower case and has to be two letters or more, there are at least three
// words, and the words are on average longer than three letters. A random
// string that happens to alternate cases has short words and fails that.
func acronymWord(w string) bool {
	w = strings.TrimRight(w, "0123456789")
	n := len(w)
	if n < 8 {
		return false
	}
	for i := 0; i < n; i++ {
		if !isLower(w[i]) && !isUpper(w[i]) {
			return false
		}
	}
	i, words, acros := 0, 0, 0
	for i < n && isLower(w[i]) {
		i++
	}
	if i == 1 {
		return false
	}
	if i > 0 {
		words++
	}
	for i < n {
		j := i
		for j < n && isUpper(w[j]) {
			j++
		}
		k := j - i
		switch {
		case j == n && k >= 2: // an acronym that ends the name
			acros++
			words++
			i = j
		case j == n: // a lone capital at the end is not a word
			return false
		case k >= 3: // an acronym, then a Capitalised word that starts at its last letter
			acros++
			words++
			i = j - 1
			fallthrough
		default:
			l := i + 1
			for l < n && isLower(w[l]) {
				l++
			}
			if l == i+1 {
				return false
			}
			words++
			i = l
		}
	}
	return acros >= 1 && words >= 3 && n*2 >= words*7
}

// domainLike is a host name or the first part of an import path:
// github.com, example.co.uk.
var domainLike = regexp.MustCompile(`^[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}$`)

// splitAny splits s at any of the characters in seps, keeping where each part
// starts.
func splitAny(s, seps string) (parts []string, at []int) {
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || strings.IndexByte(seps, s[i]) >= 0 {
			parts, at = append(parts, s[start:i]), append(at, start)
			start = i + 1
		}
	}
	return parts, at
}

// nameLike reports whether a part of a path is a name: its words, split at
// underscores, dashes and dots, are ordinary words with digits at the end. A
// random token's pieces are not.
func nameLike(part string) bool {
	words, _ := splitAny(part, "_-.")
	for _, w := range words {
		if w != "" && !isNameWord(w) {
			return false
		}
	}
	return true
}

// driveRe is the start of a Windows path: a drive letter, an environment
// variable such as %LOCALAPPDATA%, or a UNC share.
var driveRe = regexp.MustCompile(`^(?:[A-Za-z]:[\\/]|%[A-Za-z_][A-Za-z0-9_]*%[\\/]|\\\\)`)

// hexish is a part of a path that is a short hash or a UUID: hex digits and the
// dashes, dots and underscores that go with them. It stays in a path.
var hexish = regexp.MustCompile(`^[0-9A-Fa-f][0-9A-Fa-f_.\-]*$`)

// pathLike is a run that is a path or an import path rather than a token. It
// starts at a known root or at ./, ../ or ~/, or its first parts name a host
// (github.com/aws/...), or at least three fifths of its parts are names or short
// hashes and it has two separators (or all of them are). A key that happens to
// contain slashes, or one that starts with one, is none of these.
func pathLike(tok string) bool {
	if pathRoot.MatchString(tok) {
		return true
	}
	for _, p := range []string{"./", "../", "~/", `.\`, `..\`, `~\`} {
		if strings.HasPrefix(tok, p) {
			return true
		}
	}
	parts, _ := splitAny(tok, `/\`)
	if len(parts) < 2 {
		return false
	}
	for i, p := range parts {
		if i < 3 && domainLike.MatchString(p) {
			return true
		}
	}
	plain, total := 0, 0
	for _, p := range parts {
		if p == "" {
			continue
		}
		total++
		if nameLike(p) || hexish.MatchString(p) {
			plain++
		}
	}
	if total == 0 {
		return false
	}
	return plain == total || (len(parts) >= 3 && plain*5 >= total*3)
}

// identLike reports whether a token is a qualified identifier: its parts, split at
// dots, slashes, colons, dashes and underscores, are each words with digits in
// them that a programmer writes (skipUint8LengthPrefixed, x509.ParsePKCS8PrivateKey):
// letters and digits only, each run of letters a name (lower, Capitalised,
// camelCase or UPPER), and no more than three digit runs of three digits. A
// random token's letters are not words.
func identLike(tok string) bool {
	parts, _ := splitAny(tok, `./\:@_-`)
	for _, p := range parts {
		if p != "" && !identPart(p) {
			return false
		}
	}
	return true
}

func identPart(p string) bool {
	digitRuns := 0
	for i := 0; i < len(p); {
		j := i
		if p[i] >= '0' && p[i] <= '9' {
			for j < len(p) && p[j] >= '0' && p[j] <= '9' {
				j++
			}
			digitRuns++
			if j-i > 3 || digitRuns > 3 {
				return false
			}
		} else {
			for j < len(p) && (p[j] >= 'A' && p[j] <= 'Z' || p[j] >= 'a' && p[j] <= 'z') {
				j++
			}
			if j == i || !isNameWord(p[i:j]) {
				return false
			}
		}
		i = j
	}
	return true
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return s != ""
}

// entropy is the Shannon entropy of s in bits per byte.
func entropy(s string) float64 {
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	var h float64
	n := float64(len(s))
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// randomRun is the test of a run on its own: long, a mix of upper case, lower
// case and digits, and more varied than words are. It is tuned to leave a false
// positive (which costs the person an edit) over a false negative (which costs
// a secret).
//
// A long run of hex is a digest or a key. 40 characters (a commit id) and 32 (a
// UUID without its dashes, or an MD5) are kept, because a baton is full of them
// and they are not secrets; 64 and over are scrubbed, which also takes a
// SHA-256 object id or a docker image digest. That is the trade.
func randomRun(tok string) bool {
	if len(tok) < 24 {
		return false
	}
	if len(tok) >= 64 && isHex(tok) {
		return true
	}
	// An identifier or a branch name is made of words, however long and however
	// many capitals and digits it has.
	if nameLike(tok) || identLike(tok) {
		return false
	}
	var upper, lower, digit bool
	for i := 0; i < len(tok); i++ {
		switch c := tok[i]; {
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= '0' && c <= '9':
			digit = true
		}
	}
	if !upper || !lower || !digit {
		return false
	}
	min := 4.0
	if len(tok) >= 32 {
		min = 4.2
	}
	return entropy(tok) >= min
}

// looksRandom is randomRun for a whole token: a path, an import path or a URL
// is not taken for a random run.
func looksRandom(tok string) bool {
	if strings.ContainsAny(tok, `/\`) && pathLike(tok) {
		return false
	}
	return randomRun(tok)
}

// entropySpans finds the stretches of text that look random, as [start, end)
// pairs. A URL is looked at in pieces after its host, so that its host and
// scheme are kept and only a piece that looks like a key goes; a path or an
// import path is kept except for a part of it that looks like a key; and a
// word is split at = and : (key=value, sha256:digest) and at dots.
func entropySpans(text string) [][2]int {
	var out [][2]int
	for _, m := range wordRe.FindAllStringIndex(text, -1) {
		out = append(out, wordSpans(text[m[0]:m[1]], m[0])...)
	}
	return out
}

func wordSpans(w string, base int) [][2]int {
	if loc := urlRe.FindStringIndex(w); loc != nil {
		rest := w[loc[1]:]
		hostEnd := strings.IndexAny(rest, "/?#")
		if hostEnd < 0 {
			return nil
		}
		return partSpans(rest[hostEnd:], base+loc[1]+hostEnd, "/?&=#:", 24)
	}
	var out [][2]int
	parts, at := splitAny(w, "=")
	for i, p := range parts {
		out = append(out, valueSpans(p, base+at[i])...)
	}
	return out
}

// valueSpans looks at what is on one side of an = in a word. A path is kept
// whole (see pathSpans); anything else is split at : (sha256:digest, host:port)
// and each piece is looked at.
func valueSpans(p string, base int) [][2]int {
	if driveRe.MatchString(p) || (strings.ContainsAny(p, `/\`) && pathLike(p)) {
		return pathSpans(p, base)
	}
	var out [][2]int
	parts, at := splitAny(p, ":")
	for i, part := range parts {
		out = append(out, pieceSpans(part, base+at[i])...)
	}
	return out
}

// pathSpans looks at a path: its structure is kept, and only a part of it that
// is itself a long random run is taken. Parts are split at dots too, so a name
// like Microsoft.WindowsTerminal_8wekyb3d8bbwe is not judged as one run. A UUID
// or a short hash in a path stays: it names a folder, and a baton is full of
// them.
func pathSpans(p string, base int) [][2]int {
	var out [][2]int
	parts, at := splitAny(p, `/\`)
	for i, part := range parts {
		// The whole part first: a long random token with dots in it is one
		// secret, and splitting it at the dots would leave every piece too short.
		if len(part) >= pathPartMin && randomRun(part) {
			out = append(out, [2]int{base + at[i], base + at[i] + len(part)})
			continue
		}
		out = append(out, partSpans(part, base+at[i], ".:@", pathPartMin)...)
	}
	return out
}

// pieceSpans looks at one piece of a word.
func pieceSpans(p string, base int) [][2]int {
	if len(p) < 24 {
		return nil
	}
	switch {
	case strings.ContainsAny(p, `/\`):
		if pathLike(p) {
			return pathSpans(p, base)
		}
		if randomRun(p) {
			return [][2]int{{base, base + len(p)}}
		}
	case strings.Contains(p, "."):
		return partSpans(p, base, ".", 24)
	case randomRun(p):
		return [][2]int{{base, base + len(p)}}
	}
	return nil
}

// partSpans returns the parts of p, split at seps, that look random on their own
// and are at least min bytes long.
func partSpans(p string, base int, seps string, min int) [][2]int {
	var out [][2]int
	parts, at := splitAny(p, seps)
	for i, part := range parts {
		if len(part) >= min && randomRun(part) {
			out = append(out, [2]int{base + at[i], base + at[i] + len(part)})
		}
	}
	return out
}
