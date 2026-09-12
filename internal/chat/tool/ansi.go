package tool

import (
	"regexp"
	"strings"
)

// escapeSequence matches what a program writes to a terminal besides text: a
// CSI sequence -- a colour, a cursor move, a line cleared -- and an OSC one --
// a window title, a link -- ended by BEL or by ST.
var escapeSequence = regexp.MustCompile(`\x1b\[[0-9;?<=>!]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// stripANSI is s with its escape sequences taken out and the text they
// surrounded kept. A command's output goes to the model, which reads a colour
// code as noise it pays for, and to the pane, which draws it as marks.
func stripANSI(s string) string {
	return escapeSequence.ReplaceAllString(s, "")
}

// settleLines is s as a terminal would leave it showing: each line only what
// follows its last carriage return. A progress bar redraws its line with one
// -- "10%\r20%\r...100%" -- and kept whole, the model read every step of it and
// /output drew a hundred lines where the terminal had shown one. A line's CRLF
// ending is not a redraw, and is left as it is.
func settleLines(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		body, crlf := strings.CutSuffix(line, "\r")
		if j := strings.LastIndex(body, "\r"); j >= 0 {
			body = body[j+1:]
		}
		if crlf {
			body += "\r"
		}
		lines[i] = body
	}
	return strings.Join(lines, "\n")
}
