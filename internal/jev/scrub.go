package jev

import (
	"regexp"
	"strings"

	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/secretname"
)

// Scrub is what text from the user's machine goes through before it is given to
// Client.Ask as the state: terminal output, or a prompt that may have had a
// secret pasted into it.
//
// It uses the code the rest of Flockdeck uses for the same job, and adds
// passes of its own for what a terminal shows:
//
//   - A token that a terminal hard-wrapped across lines is joined for the look
//     (unwrap), so that a key broken at column 80 is still seen whole.
//   - record.Redact takes out what looks like a secret: provider key shapes,
//     bearer and basic credentials, user:password in a URL, private key blocks,
//     and the value of a NAME=value or "name": "value" pair whose name says it
//     is a secret.
//   - A word that is a path with a secret file in it (by internal/secretname:
//     .env, id_rsa, ~/.aws/credentials) is replaced with the same mark.
//   - Three or more lines in a row that look like base64 are replaced. A private
//     key is found by its BEGIN line, which the window of output that is read
//     can start after; its body is these lines.
//
// It is best effort and by pattern. A secret with no recognisable shape, on a
// line that does not name it as one, goes through as written.
//
// The result is "" when nothing is left of text but marks and white space,
// since a caller then has nothing worth asking about.
func Scrub(text string) string {
	text = unwrap(text)
	text = record.Redact(text)
	text = scrubPaths(text)
	text = scrubBase64Runs(text)
	if strings.TrimSpace(strings.ReplaceAll(text, record.Redacted, "")) == "" {
		return ""
	}
	return text
}

// scrubPaths replaces each white-space or quote separated word that has a path
// separator in it, or starts with a dot, and names a secret file. Punctuation
// after the name (".env:", ".env,") is not part of it.
func scrubPaths(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		w := s[start:end]
		core := strings.TrimRight(w, ".:!?")
		if (strings.ContainsAny(core, `/\`) || len(core) > 1 && core[0] == '.') && secretname.Path(core) {
			b.WriteString(record.Redacted)
			b.WriteString(w[len(core):])
		} else {
			b.WriteString(w)
		}
		start = -1
	}
	for i := 0; i < len(s); i++ {
		if isWordBreak(s[i]) {
			flush(i)
			b.WriteByte(s[i])
			continue
		}
		if start < 0 {
			start = i
		}
	}
	flush(len(s))
	return b.String()
}

func isWordBreak(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '"', '\'', '`', '(', ')', '<', '>', ',', ';', '[', ']', '{', '}':
		return true
	}
	return false
}

// A line of base64 as a key file writes it: 60 characters or more of the
// alphabet, nothing else.
var (
	base64Line = regexp.MustCompile(`^[A-Za-z0-9+/]{60,}={0,2}$`)
	base64Frag = regexp.MustCompile(`^[A-Za-z0-9+/]{16,}={0,2}$`)
)

// base64Floor is how many such lines in a row are a block of base64.
const base64Floor = 3

// scrubBase64Runs replaces runs of base64Floor lines or more that are base64
// (one line of it is a hash, an id or a path; a block of it is a key or a
// certificate). The line before the run is taken too when it is a stretch of
// the alphabet at least 16 long, as the first line of a body cut by the window
// is.
func scrubBase64Runs(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	isB64 := func(l string) bool { return base64Line.MatchString(strings.TrimSpace(l)) }
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if !isB64(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && isB64(lines[j]) {
			j++
		}
		if j-i < base64Floor {
			out = append(out, lines[i:j]...)
			i = j
			continue
		}
		if n := len(out); n > 0 && base64Frag.MatchString(strings.TrimSpace(out[n-1])) {
			out = out[:n-1]
		}
		out = append(out, record.Redacted)
		i = j
	}
	return strings.Join(out, "\n")
}

// tokenByte is a byte a key is made of, as far as a line break in the middle of
// one is concerned.
func tokenByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		c == '_' || c == '-' || c == '.'
}

// wrapMin is how long the end of a line must be of token characters before the
// start of the next is taken for its continuation. Terminals wrap at 80
// columns or more, and no ordinary line ends in a run this long.
const wrapMin = 40

// wrapWidth is how long a line must be to have been wrapped at all: the
// narrowest terminal worth the name is a little over this. It keeps a short
// line that ends in a key from taking the first word of the next line with it.
const wrapWidth = 60

// unwrap redacts a token that a terminal wrapped onto the next line or lines:
// where the end of a line is wrapMin or more token characters and the start of
// the next is more of them, and the two together are a token of a known shape
// (record.FindTokens), the end of the first and the start of the next are
// replaced. It does that on the text as it is, before record.Redact has taken
// the first half of a wrapped key and left the second.
func unwrap(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := 0; i+1 < len(lines); i++ {
		cr := ""
		core := lines[i]
		if strings.HasSuffix(core, "\r") {
			core, cr = core[:len(core)-1], "\r"
		}
		k := len(core)
		for k > 0 && tokenByte(core[k-1]) {
			k--
		}
		tail := core[k:]
		if len(tail) < wrapMin || len(core) < wrapWidth {
			continue
		}
		joined := tail
		type part struct{ line, n int }
		var parts []part
		for j := i + 1; j < len(lines); j++ {
			nc := strings.TrimSuffix(lines[j], "\r")
			h := 0
			for h < len(nc) && tokenByte(nc[h]) {
				h++
			}
			// The rest of a wrapped token is the whole of its line: a line
			// that goes on into words ("Continue? (y/n)") is not one.
			if h == 0 || strings.TrimSpace(nc[h:]) != "" {
				break
			}
			joined += nc[:h]
			parts = append(parts, part{j, h})
			if h < wrapWidth {
				break
			}
		}
		if len(parts) == 0 || len(record.FindTokens(joined)) == 0 {
			continue
		}
		lines[i] = core[:k] + record.Redacted + cr
		for _, p := range parts {
			nc := strings.TrimSuffix(lines[p.line], "\r")
			lines[p.line] = record.Redacted + nc[p.n:] + lines[p.line][len(nc):]
		}
	}
	return strings.Join(lines, "\n")
}
