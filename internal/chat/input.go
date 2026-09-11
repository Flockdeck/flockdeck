package chat

import (
	"bufio"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"
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
	// interrupted is when the user last pressed Ctrl+C, in Unix nanoseconds,
	// and 0 once an end of input has been put down to it.
	interrupted atomic.Int64
}

// maxLine bounds one line. It is generous because a line is often a paste --
// an error, a log, a whole file -- and a paste cut in half is worse than a
// paste that is too long.
const maxLine = 8 << 20

// interruptGrace is how long an end of input waits to learn whether it was
// really a Ctrl+C, whose interrupt can arrive a moment either side of it.
const interruptGrace = 300 * time.Millisecond

// newInput starts reading r. console says r is the terminal itself.
func newInput(r io.Reader, console bool) *input {
	in := &input{lines: make(chan string), closed: make(chan struct{})}
	go func() {
		defer close(in.closed)
		for {
			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 0, 64<<10), maxLine)
			for sc.Scan() {
				// A pane on Windows delivers CRLF; the carriage return is not
				// part of what the user typed.
				in.lines <- strings.TrimRight(sc.Text(), "\r")
			}
			// On a Windows console a Ctrl+C ends the read it arrives during
			// as though the input had ended, as well as raising the interrupt,
			// and the console goes on reading lines afterwards. Taken at its
			// word, it would close the chat on the key that is promised only
			// to stop an answer.
			if sc.Err() != nil || !console || !in.wasCtrlC() {
				return
			}
		}
	}()
	return in
}

// interrupt notes that the user pressed Ctrl+C.
func (in *input) interrupt() { in.interrupted.Store(time.Now().UnixNano()) }

// wasCtrlC reports whether an end of input came with a Ctrl+C rather than the
// input really ending. Each Ctrl+C accounts for one end of input and no more,
// so a terminal that has really gone is still noticed.
func (in *input) wasCtrlC() bool {
	deadline := time.Now().Add(interruptGrace)
	for {
		t := in.interrupted.Load()
		if t != 0 && time.Since(time.Unix(0, t)) < 2*interruptGrace && in.interrupted.CompareAndSwap(t, 0) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// isConsole reports whether r is a terminal rather than a pipe or a file.
func isConsole(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
