package record

import (
	"math/rand"
	"strings"
	"testing"
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
