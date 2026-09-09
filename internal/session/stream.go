package session

import "bytes"

// ring is a fixed-size circular buffer of recent output. It keeps the most
// recent replayBytes of a pane so a viewer that connects late, or reloads, can
// rebuild its screen by replaying them into a fresh terminal.
type ring struct {
	buf  []byte
	pos  int
	full bool
}

func newRing(size int) *ring {
	return &ring{buf: make([]byte, size)}
}

// write appends p, discarding the oldest bytes once the buffer is full.
func (r *ring) write(p []byte) {
	if len(r.buf) == 0 {
		return
	}
	// Only the tail can survive when the input is larger than the buffer.
	if len(p) >= len(r.buf) {
		copy(r.buf, p[len(p)-len(r.buf):])
		r.pos = 0
		r.full = true
		return
	}
	n := copy(r.buf[r.pos:], p)
	if n < len(p) {
		copy(r.buf, p[n:])
		r.full = true
	}
	r.pos = (r.pos + len(p)) % len(r.buf)
	if r.pos == 0 {
		r.full = true
	}
}

// bytes returns the buffered output in order, oldest first.
func (r *ring) bytes() []byte {
	if !r.full {
		out := make([]byte, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}
	out := make([]byte, 0, len(r.buf))
	out = append(out, r.buf[r.pos:]...)
	out = append(out, r.buf[:r.pos]...)
	return out
}

// replay returns the buffered output for a viewer rebuilding its screen.
//
// Once the buffer has wrapped its oldest byte is wherever the last write
// happened to land, which is usually the middle of an escape sequence: the
// viewer feeds that to a fresh terminal, which has no sequence to attach the
// parameters to and prints them, so every reload of a pane that has said more
// than a bufferful opens with "38;5;42m" where a word should be. Starting at
// a line boundary costs at most one line of scrollback.
func (r *ring) replay() []byte {
	out := r.bytes()
	if r.full {
		out = dropPartialLine(out)
	}
	return out
}

// tail returns the last n bytes written, oldest first, and reports whether
// anything older than them was left behind. n of zero or less means all of it.
//
// Reading a tail through bytes() would copy the whole buffer to keep a
// fraction of it, and the buffer is half a megabyte for every open pane.
func (r *ring) tail(n int) (b []byte, truncated bool) {
	held := r.pos
	if r.full {
		held = len(r.buf)
	}
	if n <= 0 || n >= held {
		return r.bytes(), false
	}
	out := make([]byte, 0, n)
	start := (r.pos - n + len(r.buf)) % len(r.buf)
	if start+n <= len(r.buf) {
		out = append(out, r.buf[start:start+n]...)
	} else {
		out = append(out, r.buf[start:]...)
		out = append(out, r.buf[:n-(len(r.buf)-start)]...)
	}
	return out, true
}

// bellScanner finds terminal bells in a byte stream.
//
// A naive search for 0x07 is wrong: BEL is also the terminator of an OSC
// sequence, and Claude Code sets the window title that way, so every title
// change would look like a request for attention. This tracks whether the
// stream is inside a string sequence and ignores the BEL that ends one.
//
// It is only ever driven from the single PTY reader goroutine, so it needs no
// locking of its own.
type bellScanner struct {
	state scanState
}

type scanState int

const (
	scanNormal scanState = iota
	scanEsc
	scanOSC
	scanOSCEsc
	scanCSI
	scanEscArg
)

// scan reports whether p contains a real bell.
func (b *bellScanner) scan(p []byte) bool {
	rang := false
	for _, c := range p {
		switch b.state {
		case scanNormal:
			switch c {
			case 0x1b:
				b.state = scanEsc
			case 0x07:
				rang = true
			}
		case scanEsc:
			switch c {
			// OSC, and the other string sequences (DCS, APC, PM), all carry a
			// payload that xterm lets a BEL terminate, so all of them have to
			// be tracked or the byte that ends one reads as a request for
			// attention.
			case ']', 'P', '^', '_':
				b.state = scanOSC
			case 0x1b:
				// A second escape restarts the sequence rather than being the
				// body of the first.
			default:
				// Any other escape sequence (CSI included) is terminated by a
				// byte that cannot be BEL, so tracking it adds nothing.
				b.state = scanNormal
			}
		case scanOSC:
			switch c {
			case 0x07:
				b.state = scanNormal // terminates the OSC; not a bell
			case 0x1b:
				b.state = scanOSCEsc
			}
		case scanOSCEsc:
			if c == '\\' {
				b.state = scanNormal // ST terminator
			} else {
				b.state = scanOSC
			}
		}
	}
	return rang
}

// stripANSI removes escape sequences from terminal output, leaving the text a
// person would have read on screen.
//
// It is deliberately small: the goal is to recover prose and list items from a
// pane's recent output, not to emulate a terminal.
func stripANSI(p []byte) string {
	out := make([]byte, 0, len(p))
	// lineStart is where the line currently being written began, which is
	// where a carriage return goes back to.
	lineStart := 0
	breakLine := func() {
		out = append(out, '\n')
		lineStart = len(out)
	}

	state := scanNormal
	var params []byte
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch state {
		case scanNormal:
			switch {
			case c == 0x1b:
				state = scanEsc
			case c == 0x07, c == 0x00:
				// Bells and padding are not text.
			case c == '\r':
				// Before a newline a carriage return is only part of the line
				// break. On its own it rewinds to the start of the line and
				// what follows takes the place of what was there, which is
				// how a progress line redraws itself; keeping both is how the
				// screen's "100%" is read back as "50%100%".
				if i+1 < len(p) && p[i+1] == '\n' {
					break
				}
				out = out[:lineStart]
			case c == '\n':
				breakLine()
			case c < 0x20 && c != '\t':
			default:
				out = append(out, c)
			}
		case scanEsc:
			switch c {
			case '[':
				state = scanCSI
				params = params[:0]
			case ']':
				state = scanOSC
			case 'P', '^', '_':
				state = scanOSC // string sequences end the same way
			// A designator takes one more byte to say which set it selects,
			// and that byte is a letter or a digit. Dropping only the escape
			// leaves it behind, which is how "ESC ( B" -- how a program says
			// it is back to plain ASCII, printed constantly -- turns into a
			// stray "B" in the middle of a sentence.
			case '(', ')', '*', '+', '-', '.', '/', '#', '%', ' ':
				state = scanEscArg
			default:
				state = scanNormal
			}
		case scanEscArg:
			state = scanNormal
		case scanCSI:
			// A CSI sequence ends at its final byte; the bytes before it are
			// the parameters, which matter for the moves that stand in for
			// text.
			if c < 0x40 || c > 0x7e {
				if len(params) < 16 {
					params = append(params, c)
				}
				break
			}
			state = scanNormal
			switch {
			// A terminal application often moves the cursor to the next
			// line rather than printing a newline, so dropping these
			// outright would run separate lines together. Treat the
			// movement as the line break it stands for.
			case breaksLine(c):
				if len(out) > 0 && out[len(out)-1] != '\n' {
					breakLine()
				}
			// Moving the cursor right is how a full-screen program draws a run
			// of blanks without printing them. Dropping the move runs the words
			// on either side of it together, which is how a sentence ends up
			// here as onelongword.
			case c == 'C':
				for n := csiCount(params); n > 0; n-- {
					out = append(out, ' ')
				}
			// Moving to a column is the other way of saying what a carriage
			// return says, and the way Claude Code's own interface says it:
			// go back to the start of the line and draw it again. Without
			// this the line before the redraw and the line after it are read
			// as one, which is how a status line that has counted to a
			// hundred arrives here as every number it passed through.
			case c == 'G':
				col := csiCount(params)
				if col < 1 {
					col = 1
				}
				target := lineStart + col - 1
				for len(out) < target {
					out = append(out, ' ')
				}
				out = out[:target]
			// Erasing the line is the rest of that idiom. Only erasing all of
			// it has anything to undo where text is appended rather than laid
			// out: the other forms erase what has not been written yet.
			case c == 'K' && csiCount(params) == 2:
				out = out[:lineStart]
			}
		case scanOSC:
			if c == 0x07 {
				state = scanNormal
			} else if c == 0x1b {
				state = scanOSCEsc
			}
		case scanOSCEsc:
			state = scanNormal
		}
	}
	return string(out)
}

// maxCSICount bounds a cursor move that stands in for blanks. No terminal is
// this wide, so a larger parameter is corruption rather than layout.
const maxCSICount = 500

// csiCount reads the numeric argument of a CSI sequence. Absent means one.
func csiCount(params []byte) int {
	n, digits := 0, 0
	for _, c := range params {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
		digits++
		if n >= maxCSICount {
			return maxCSICount
		}
	}
	if digits == 0 {
		return 1
	}
	return n
}

// breaksLine reports whether a CSI final byte represents moving off the
// current line. Horizontal moves and erase-in-line do not.
func breaksLine(final byte) bool {
	switch final {
	case 'H', 'f', // cursor position
		'A', 'B', // up, down
		'E', 'F', // next line, previous line
		'J': // erase in display
		return true
	}
	return false
}

// RecentText returns the tail of the pane's output as plain text, with escape
// sequences removed. It is what the fan-out dialog reads to find the work a
// lead agent has proposed.
func (s *Session) RecentText(maxBytes int) string {
	s.mu.RLock()
	raw, truncated := s.history.tail(maxBytes)
	s.mu.RUnlock()

	if truncated {
		raw = dropPartialLine(raw)
	}
	return stripANSI(raw)
}

// dropPartialLine drops everything up to and including the first line break,
// which is the line a byte-counted tail was cut in the middle of.
//
// A line of terminal output is mostly escape sequences: opening inside one
// leaves the reader looking at "38;5;42mgreen" where it expected a word, and
// can halve a UTF-8 rune besides. Output holding no line break at all is
// better shown truncated than dropped entirely.
func dropPartialLine(p []byte) []byte {
	if i := bytes.IndexByte(p, '\n'); i >= 0 {
		return p[i+1:]
	}
	return p
}
