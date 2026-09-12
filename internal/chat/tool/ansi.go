package tool

import "regexp"

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
