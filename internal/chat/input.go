package chat

import (
	"bufio"
	"io"
	"strings"
)

// input reads what the user types, one line at a time, on a goroutine of its
// own.
//
// It is a goroutine and a channel rather than a read where the line is wanted
// because two places wait for a line -- the prompt, and a tool asking
// permission -- and both must be able to give up on it: a cancelled turn cannot
// leave the whole client blocked in a read until somebody presses return.
//
// Lines rather than keystrokes because putting the terminal into raw mode needs
// an ioctl, which the standard library cannot make; the terminal's own line
// editing is what a chat client would want most of anyway.
type input struct {
	lines chan string
	// closed is shut when there will be no more input: the pane has gone, or
	// the user pressed the key for end of file.
	closed chan struct{}
}

// maxLine bounds one line. It is generous because a line is often a paste --
// an error, a log, a whole file -- and a paste cut in half is worse than a
// paste that is too long.
const maxLine = 8 << 20

func newInput(r io.Reader) *input {
	in := &input{lines: make(chan string), closed: make(chan struct{})}
	go func() {
		defer close(in.closed)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64<<10), maxLine)
		for sc.Scan() {
			// A pane on Windows delivers CRLF; the carriage return is not part
			// of what the user typed.
			in.lines <- strings.TrimRight(sc.Text(), "\r")
		}
	}()
	return in
}
