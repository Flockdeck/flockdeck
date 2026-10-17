package jev

import (
	"strings"

	"github.com/jmwri/flockdeck/internal/record"
	"github.com/jmwri/flockdeck/internal/secretname"
)

// Scrub is what text from the user's machine goes through before it is given to
// Client.Ask as the state: terminal output, or a prompt that may have had a
// secret pasted into it.
//
// It does two things, both with the code the rest of Flockdeck uses for the
// same job. record.Redact takes out what looks like a secret: provider key
// shapes, bearer and basic credentials, user:password in a URL, private key
// blocks, and the value of a NAME=value or "name": "value" pair whose name says
// it is a secret. Then any word that is a path with a secret file in it (by
// internal/secretname: .env, id_rsa, ~/.aws/credentials) is replaced with the
// same mark, so that the name of a secret file is not sent either.
//
// It is best effort and by pattern. A secret with no recognisable shape, on a
// line that does not name it as one, goes through as written.
//
// The result is "" when nothing is left of text but marks and white space,
// since a caller then has nothing worth asking about.
func Scrub(text string) string {
	text = record.Redact(text)
	text = scrubPaths(text)
	if strings.TrimSpace(strings.ReplaceAll(text, record.Redacted, "")) == "" {
		return ""
	}
	return text
}

// scrubPaths replaces each white-space or quote separated word that has a path
// separator in it, or starts with a dot, and names a secret file.
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
		if (strings.ContainsAny(w, `/\`) || len(w) > 1 && w[0] == '.') && secretname.Path(w) {
			b.WriteString(record.Redacted)
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
