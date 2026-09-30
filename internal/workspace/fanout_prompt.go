package workspace

import (
	"regexp"
	"strings"
)

// promptTailLines is how many of a screen's last non-blank lines are looked at
// for a permission prompt: the question, a few options, and the hint under them.
const promptTailLines = 12

// promptOption is a numbered choice as Claude Code draws it, with or without
// its pointer: "❯ 1. Yes", "  2. Yes, and don't ask again for go run".
var promptOption = regexp.MustCompile(`^[❯>›»]?\s*(\d{1,2})[.)]\s+(\S.*)$`)

// endsInPermissionPrompt reports whether the last thing on a pane's screen is
// an agent asking for permission: numbered choices that begin with "Yes" and
// include a "No", with nothing after them but a hint line.
//
// Those choices are a numbered list like any other, and a screen is read for
// lists. Read as a plan, "1. Yes / 2. Yes, and don't ask again / 3. No" offered
// three agents to start, the last of them told to refuse. They are a question
// for the person, not work, and only while they are the last output: once the
// question has been answered the screen moves on, and what is above it is read
// as before.
func endsInPermissionPrompt(screen string) bool {
	var tail []string
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0 && len(tail) < promptTailLines; i-- {
		// A prompt drawn in a frame has its sides in the line.
		line := strings.TrimSpace(strings.Trim(strings.TrimSpace(lines[i]), "│┃|"))
		if line != "" {
			tail = append([]string{line}, tail...)
		}
	}
	// Walk back over the last run of choices: up to three lines after it (the
	// hint under a prompt, or the wrapped end of its last choice), and one line
	// between two choices where a long one wrapped. A plan above an answered
	// prompt is a run of its own, and the old prompt's choices are not part of it.
	yes, no, seen := false, false, false
	after, gap := 0, 0
	for i := len(tail) - 1; i >= 0; i-- {
		m := promptOption.FindStringSubmatch(tail[i])
		if m == nil {
			if !seen {
				if after++; after > 3 {
					return false
				}
			} else if gap++; gap > 1 {
				break
			}
			continue
		}
		seen, gap = true, 0
		text := strings.ToLower(m[2])
		yes = yes || (m[1] == "1" && strings.HasPrefix(text, "yes"))
		no = no || strings.HasPrefix(text, "no")
	}
	return yes && no
}
