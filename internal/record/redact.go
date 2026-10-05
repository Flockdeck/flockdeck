package record

import (
	"fmt"
	"regexp"
	"sort"
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

	// Authorization headers: the scheme is kept, the credential is not.
	bearerRe = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9._~+/=\-]{12,}`)
)

// shape is a token with a recognisable shape, whatever it is called, and the
// kind of secret a caller can say it was.
type shape struct {
	kind string
	re   *regexp.Regexp
}

var tokenShapes = []shape{
	{"anthropic-key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{10,}`)},
	{"api-key", regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`)},
	{"github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`)},
	{"github-token", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{"gitlab-token", regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{20,}`)},
	{"aws-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"slack-token", regexp.MustCompile(`xox[abprs]-[A-Za-z0-9\-]{10,}`)},
	{"google-key", regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`)},
	{"npm-token", regexp.MustCompile(`npm_[A-Za-z0-9]{36}`)},
	{"sendgrid-key", regexp.MustCompile(`\bSG\.[A-Za-z0-9_\-]{16,}\.[A-Za-z0-9_\-]{16,}`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`)},
}

// Kinds lists every kind of secret Find names: the kinds of the shaped tokens
// and the four it gives to the rest. It is for a caller that marks what it
// removes and must know the marks it can have made.
func Kinds() []string {
	kinds := []string{"private-key", "bearer-token", "url-credential", "secret-value"}
	seen := map[string]bool{}
	for _, k := range kinds {
		seen[k] = true
	}
	for _, t := range tokenShapes {
		if !seen[t.kind] {
			seen[t.kind] = true
			kinds = append(kinds, t.kind)
		}
	}
	return kinds
}

// SecretName reports whether a variable or field name says that what it holds
// is a secret.
func SecretName(name string) bool { return secretNameRe.MatchString(name) }

// Span is a stretch of a string that Find took for a secret: the bytes from
// Start up to End, and the kind of secret it looks like.
type Span struct {
	Start, End int
	Kind       string
}

// FindTokens reports the tokens in s that have a recognisable shape (a
// provider's key prefix, a JWT) and nothing that is only named as a secret.
// It is the part of Find that is safe to run on text that has been joined
// across line breaks.
func FindTokens(s string) []Span {
	var out []Span
	for _, t := range tokenShapes {
		for _, m := range t.re.FindAllStringIndex(s, -1) {
			out = append(out, Span{m[0], m[1], t.kind})
		}
	}
	return merge(out)
}

// Find reports what Redact would replace in s, as spans of s, without
// replacing it. Spans that overlap are merged into one.
func Find(s string) []Span {
	return merge(append(findOthers(s), FindTokens(s)...))
}

// findOthers is Find without the shaped tokens: private keys, credentials after
// Bearer and Basic and in URLs, and values assigned to a secret's name.
func findOthers(s string) []Span {
	if s == "" {
		return nil
	}
	var out []Span
	for _, m := range pemRe.FindAllStringIndex(s, -1) {
		out = append(out, Span{m[0], m[1], "private-key"})
	}
	// The scheme and the space after it stay; the credential goes.
	for _, m := range bearerRe.FindAllStringSubmatchIndex(s, -1) {
		out = append(out, Span{m[5], m[1], "bearer-token"})
	}
	// From after the colon that follows the user, up to the "@".
	for _, m := range urlCredRe.FindAllStringSubmatchIndex(s, -1) {
		out = append(out, Span{m[3] + 1, m[1] - 1, "url-credential"})
	}
	for _, m := range assignRe.FindAllStringSubmatchIndex(s, -1) {
		if strings.Contains(s[m[4]:m[5]], Redacted) || isScheme(s[m[2]:m[3]], s[m[4]:m[5]]) {
			continue
		}
		out = append(out, Span{m[4], m[5], "secret-value"})
	}
	return merge(out)
}

// isScheme reports whether an assignment is the scheme word of an
// Authorization header: the name is that of the header and the value is
// "Bearer" or "Basic". The bearer pattern handles the credential after it, and
// "Bearer" is not the secret in "Authorization: Bearer abc...". Under any other
// name the same word is a value like any other: password=basic is a password.
func isScheme(name, value string) bool {
	name = strings.ToLower(strings.Trim(name, " \t'\":="))
	if name != "authorization" && name != "proxy-authorization" {
		return false
	}
	return strings.EqualFold(value, "bearer") || strings.EqualFold(value, "basic")
}

// merge sorts spans and joins the ones that overlap, keeping the kind of the
// first.
func merge(spans []Span) []Span {
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End > spans[j].End
	})
	out := spans[:1]
	for _, sp := range spans[1:] {
		last := &out[len(out)-1]
		if sp.Start < last.End {
			last.End = max(last.End, sp.End)
			continue
		}
		out = append(out, sp)
	}
	return out
}

// Redact removes what looks like a secret from s. It is best effort, by
// patterns: a secret with no recognisable shape, in text that does not name it
// as one, is kept as it was said.
func Redact(s string) string {
	spans := findOthers(s)
	if len(spans) > 0 {
		var b strings.Builder
		at := 0
		for _, sp := range spans {
			b.WriteString(s[at:sp.Start])
			b.WriteString(Redacted)
			at = sp.End
		}
		b.WriteString(s[at:])
		s = b.String()
	}
	// The shaped tokens go one shape after another on what is left, as they always
	// have. Finding them all on the original text and merging (Find does that, for
	// callers that need spans of the text they have) is not the same: a shape that
	// starts or ends at a word boundary, an AWS key or a JWT, sees a boundary where
	// another token that was replaced before it stood glued to it, and does not on
	// the original.
	for _, t := range tokenShapes {
		s = t.re.ReplaceAllString(s, Redacted)
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
