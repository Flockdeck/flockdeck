package record

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Redacted is what a secret is replaced with.
const Redacted = "[redacted]"

// Withheld is what the output of a read of a secret file is replaced with.
const Withheld = "[withheld: a secret file]"

// Clip cuts s to at most n bytes on a rune boundary and says how much it cut,
// so a reader of the transcript knows what they are looking at is not all of
// it.
func Clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s…[clipped %d bytes]", s[:cut], len(s)-cut)
}

// secretName is the idea of a secret's name the guard in internal/review has,
// widened from file names to variable and field names: whatever a value of this
// name holds is taken to be one.
const secretName = `(?:secret|token|passw(?:or)?d|passwd|pwd|api[_-]?key|apikey|access[_-]?key|private[_-]?key|credential|authorization|auth[_-](?:token|key|header))`

var (
	secretNameRe = regexp.MustCompile(`(?i)` + secretName)

	// A private key's whole armoured block; an unterminated one (a clipped
	// output) is taken to the end of the text.
	pemRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`)

	// NAME=value, NAME: value and "name": "value", where the name says it is a
	// secret. The value is a quoted string or the run up to whitespace or a
	// delimiter. Group 1 is kept.
	assignRe = regexp.MustCompile(`(?i)(["']?[A-Za-z0-9_.\-]*` + secretName + `[A-Za-z0-9_.\-]*["']?\s*[:=]\s*)("(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\n])*'|[^\s"',;&)}\]]+)`)

	// user:password@ in a URL; group 1 is the scheme and user.
	urlCredRe = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^\s/:@]+):[^\s/@]+@`)

	// Tokens with a recognisable shape, whatever they are called.
	tokenRes = []*regexp.Regexp{
		regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{10,}`),
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`),
		regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`),
		regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
		regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{20,}`),
		regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		regexp.MustCompile(`xox[abprs]-[A-Za-z0-9\-]{10,}`),
		regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`),
		regexp.MustCompile(`npm_[A-Za-z0-9]{36}`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`),
	}

	// Authorization headers: the scheme is kept, the credential is not.
	bearerRe = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9._~+/=\-]{12,}`)
)

// Redact removes what looks like a secret from s. It is best effort, by
// patterns: a secret with no recognisable shape, in text that does not name it
// as one, is kept as it was said.
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = pemRe.ReplaceAllString(s, Redacted)
	s = bearerRe.ReplaceAllString(s, "${1}${2}"+Redacted)
	s = urlCredRe.ReplaceAllString(s, "${1}:"+Redacted+"@")
	s = assignRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := assignRe.FindStringSubmatch(m)
		if len(sub) < 3 || strings.Contains(sub[2], Redacted) {
			return m
		}
		return sub[1] + Redacted
	})
	for _, re := range tokenRes {
		s = re.ReplaceAllString(s, Redacted)
	}
	return s
}

// RedactValue is Redact for a decoded JSON value: every string in it is
// redacted, and a value under a key that names a secret is replaced whole
// whatever it holds.
func RedactValue(v any) any {
	switch x := v.(type) {
	case string:
		return Redact(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = RedactValue(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if secretNameRe.MatchString(k) {
				out[k] = Redacted
				continue
			}
			out[k] = RedactValue(e)
		}
		return out
	}
	return v
}

// ClipValue is Clip for every string in a decoded JSON value.
func ClipValue(v any, n int) any {
	switch x := v.(type) {
	case string:
		return Clip(x, n)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = ClipValue(e, n)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = ClipValue(e, n)
		}
		return out
	}
	return v
}
