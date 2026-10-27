package workspace

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/team"
)

// Text mode types a message's text instead of the fixed wake line, for an agent
// that cannot run `flockdeck inbox read`. It is the one place a sender's words
// reach another agent's input without the person having typed them, so it is
// off until the person turns it on for an agent type, and what is typed is
// narrow: a fixed prefix naming the sender's role, then the text on one line
// with everything that is not plain text taken out, cut to a fixed length.
//
// The prefix is what keeps the text from being a command to the program. Aider
// and similar programs read a line that begins with a slash or an exclamation
// mark as a command of their own, and this line always begins with "Flockdeck".

// wakeTextAllowed says whether the wake for c carries the message text: the
// agent's catalog entry offers it and the person has opted in. Structured panes
// are not woken; their mail is always prompts that hold the text.
func wakeTextAllowed(c wakeCandidate, set WakeSettings) bool {
	return c.TextOK && set.WakeText(c.Agent)
}

// wakeTextRoleMax is the most characters of the sender's role that are typed.
const wakeTextRoleMax = 32

// wakeTextPrefix is the fixed start of a text line, up to the role.
const wakeTextPrefix = "Flockdeck message from "

// wakeTextLine is what is typed for m in text mode: the prefix, the sender's
// role, a colon and the text, all on one line of at most team.MaxTextLine
// characters. A message whose text has nothing left after cleaning is typed as
// the fixed line instead, since an empty message is no use to anyone.
func wakeTextLine(m team.Message) string {
	text := cleanWakeText(m.Text)
	if text == "" {
		return wakeLine(m.ID)
	}
	return cutRunes(wakeTextPrefix+wakeTextSender(m)+": "+text, team.MaxTextLine)
}

// wakeTextSender names the sender for the prefix: the role, else the name,
// else who it must have been, cleaned and cut.
func wakeTextSender(m team.Message) string {
	role := cleanWakeText(m.From.Role)
	if role == "" {
		role = cleanWakeText(m.From.Name)
	}
	switch {
	case role == "" && m.From.Pane == "":
		role = "the user"
	case role == "":
		role = "another agent"
	}
	return cutRunes(role, wakeTextRoleMax)
}

// wakePromptSender is the sender part of a prompt line, after the prefix.
func wakePromptSender(m team.Message) string {
	if m.From.User {
		return "the user"
	}
	label := cleanWakeText(m.From.Role)
	if label == "" {
		label = cleanWakeText(m.From.Name)
	}
	label = cutRunes(strings.ReplaceAll(label, `"`, ""), wakeTextRoleMax)
	if label == "" {
		return "an unnamed agent"
	}
	return `agent "` + label + `"`
}

// cleanWakeText turns s into one line of plain text. Terminal escape sequences
// are removed whole (a sequence cut to its first byte would leave its
// parameters behind as readable text), every run of white space, newlines
// included, becomes one space, and control, format (bidirectional overrides,
// zero width characters), surrogate, private-use and invalid characters are
// dropped. Other text, non-ASCII included, is kept as written.
func cleanWakeText(s string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == 0x1b {
			i += escapeLen(s[i:])
			continue
		}
		i += n
		switch {
		case r == utf8.RuneError && n == 1:
			continue
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Cs, unicode.Co):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// escapeLen is the length in bytes of the terminal escape sequence at the start
// of s, which begins with ESC. A sequence that never ends takes the rest of s.
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		// CSI: parameters, then a final byte from 0x40 to 0x7e.
		for i := 2; i < len(s); i++ {
			if s[i] >= 0x40 && s[i] <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']', 'P', '_', '^', 'X':
		// OSC, DCS, APC, PM and SOS: up to BEL or ST (ESC backslash).
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	// ESC and one more character: a two byte sequence, or a character set
	// designation that takes a third.
	if s[1] >= 0x20 && s[1] <= 0x2f && len(s) > 2 {
		return 3
	}
	return 2
}

// cutRunes cuts s to at most max characters.
func cutRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return strings.TrimRight(s[:i], " ")
		}
		n++
	}
	return s
}
