package team

import (
	"strings"
	"unicode"
)

// Block makes text of several lines safe to print: control characters other
// than the newline and the tab are dropped, and a carriage return, vertical
// tab, form feed or line or paragraph separator becomes a newline. Every line
// starts with indent, so a line of the text cannot pass for a line of the
// caller's own output.
func Block(s, indent string) string {
	var b strings.Builder
	b.WriteString(indent)
	cr := false
	for _, r := range s {
		if r == '\n' && cr {
			cr = false
			continue
		}
		cr = r == '\r'
		switch {
		case r == '\n' || r == '\r' || r == '\v' || r == '\f' || r == ' ' || r == ' ' || r == '\u0085':
			b.WriteString("\n" + indent)
		case r == '\t':
			b.WriteString("    ")
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
