package record

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// pemRe and assignRe are the patterns Redact was first written with. The code
// finds the same things by hand now, because a pattern with no literal to start
// from is read byte by byte; these stay as the reference it is compared with.
var (
	pemRe    = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`)
	assignRe = regexp.MustCompile(`(?i)(["']?[A-Za-z0-9_.\-]*` + secretName + `[A-Za-z0-9_.\-]*["']?\s*[:=]\s*)("(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\n])*'|[^\s"',;&)}\]]+)`)
)

// refRedact is Redact as it was before spans were found on the original text: each
// pattern replaces on what the one before it left. A token shape that starts or
// ends with \b sees the "[REDACTED]" the earlier one left as a word boundary.
func refRedact(s string) string {
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
	for _, t := range tokenShapes {
		if t.kind == "sendgrid-key" { // not in the old list
			continue
		}
		s = t.re.ReplaceAllString(s, Redacted)
	}
	return s
}

// left is what is in s once the marks are taken out.
func left(s string) string { return strings.ReplaceAll(s, Redacted, "") }

func pick(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

// tokenFrom builds a token of a recognisable shape from fragments at run time, so
// that no literal in this file looks like one to a secret scanner.
func tokenFrom(r *rand.Rand, kind int) string {
	const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const upper = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	switch kind {
	case 0:
		return "sk-" + "ant-" + pick(r, alnum, 12)
	case 1:
		return "sk-" + pick(r, alnum, 24)
	case 2:
		return "gh" + "p_" + pick(r, alnum, 32)
	case 3:
		return "AK" + "IA" + pick(r, upper, 16)
	case 4:
		return "xo" + "xb-" + pick(r, alnum, 14)
	case 5:
		return "AI" + "za" + pick(r, alnum, 35)
	case 6:
		return "npm" + "_" + pick(r, alnum, 36)
	case 7:
		return "ey" + "J" + pick(r, alnum, 10) + "." + pick(r, alnum, 10) + "." + pick(r, alnum, 10)
	default:
		return "gl" + "pat-" + pick(r, alnum, 22)
	}
}

// A token glued straight to another token that is replaced first was redacted
// before spans were found on the original text, and the \b of its shape saw the
// mark between them. The same input must come out the same.
func TestRedactGluedTokensAsBefore(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	seps := []string{"", "", "", " ", "\n", "=", ":", "-", "_", "x", "\""}
	for i := 0; i < 60000; i++ {
		var parts []string
		for n := 1 + r.Intn(3); n > 0; n-- {
			parts = append(parts, tokenFrom(r, r.Intn(9)))
			if r.Intn(6) == 0 {
				parts = append(parts, "word")
			}
		}
		var in strings.Builder
		for j, p := range parts {
			if j > 0 {
				in.WriteString(seps[r.Intn(len(seps))])
			}
			in.WriteString(p)
		}
		// Spans that overlap are one mark now, and a token inside another is taken
		// with it where it used to leave a tail. What must not happen is more of the
		// text being left than before.
		got, want := Redact(in.String()), refRedact(in.String())
		if len(left(got)) > len(left(want)) {
			t.Fatalf("Redact(%q)\n got %q\nwant %q", in.String(), got, want)
		}
	}
}

func TestRedactAKeyGluedBeforeAnotherToken(t *testing.T) {
	aws := "AK" + "IA" + "IOSFODNN7EXAMPLE"
	for name, next := range map[string]string{
		"an anthropic key": "sk-" + "ant-" + "abcdefghijkl",
		"a github token":   "gh" + "p_" + strings.Repeat("a1", 16),
		"a gitlab token":   "gl" + "pat-" + strings.Repeat("b2", 11),
	} {
		got := Redact(aws + next)
		if strings.Contains(got, "AK"+"IA") || strings.Contains(got, "sk-") || strings.Contains(got, "ghp"+"_") || strings.Contains(got, "glpat") {
			t.Errorf("%s glued after an AWS key: part was left: %q", name, got)
		}
	}
}

// secretName, passName and the two patterns made of them are the idea of a
// secret's name as it was written first, as patterns. nameIsSecret finds the same
// names by hand; the tests compare the two.
const (
	secretName = `(?:secret|token|passw(?:or)?d|passwd|pwd|api[_-]?key|apikey|access[_-]?key|private[_-]?key|credential|authorization|auth[_-](?:token|key|header))`
	passName   = `[A-Za-z0-9][^A-Za-z0-9]+pass$|(?:^|[^A-Za-z0-9])(?:db|my|app|admin|user|root|smtp|ftp|mail)pass$`
)

var (
	secretNameRe = regexp.MustCompile(`(?i)` + secretName)
	redactNameRe = regexp.MustCompile(`(?i)` + secretName + `|passphrase|` + passName)
)

func TestNameIsSecretMatchesThePatterns(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	pieces := []string{"s", "e", "c", "r", "t", "o", "k", "n", "p", "a", "w", "d", "i", "_", "-", ".", "x", "u", "h", "y", "g", "A", "P", "K",
		"secret", "token", "pass", "word", "wd", "_pass", "x-pass", "my", "db", "root", "pwd", "api", "key", "access", "private", "auth", "header", "phrase", "credential", "ization", "ſ", "K", "é", "1"}
	for n := 0; n < 200000; n++ {
		var b strings.Builder
		for i, k := 0, 1+r.Intn(5); i < k; i++ {
			b.WriteString(pieces[r.Intn(len(pieces))])
		}
		s := b.String()
		if got, want := nameIsSecret(s, false), secretNameRe.MatchString(s); got != want {
			t.Fatalf("nameIsSecret(%q, false) = %v, the pattern says %v", s, got, want)
		}
		if got, want := nameIsSecret(s, true), redactNameRe.MatchString(s); got != want {
			t.Fatalf("nameIsSecret(%q, true) = %v, the pattern says %v", s, got, want)
		}
	}
}
