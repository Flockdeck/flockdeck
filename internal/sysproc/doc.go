// Package sysproc starts background helper processes without giving each one
// a window of its own.
//
// The released Windows binary is linked as a GUI program, so that starting it
// from the Start menu does not leave a console window standing behind it. The
// cost is that it has no console for its children to share: every console
// program it starts -- git for each pane's branch label, cmd for the fallback
// browser, whatever an API agent's run_command asks for -- is handed a brand
// new console by Windows, which on Windows 11 opens as a Windows Terminal
// window. The git poll alone runs every fifteen seconds per checkout, so the
// screen fills with them and the application itself is buried underneath.
//
// A build run from a terminal never shows this, because there the children
// quietly share the terminal's console. That is why it went unnoticed until a
// released copy was started from its shortcut.
package sysproc
