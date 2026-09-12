package session

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jmwri/flockdeck/internal/agent"
)

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
// It also keeps track of the terminal modes the stream has switched, which is
// the same walk over the same bytes: see termModes.
//
// It is only ever driven from the single PTY reader goroutine, so it needs no
// locking of its own.
type bellScanner struct {
	state scanState
	// pos is how many bytes were scanned before the current chunk, and escAt
	// where the escape that began the current sequence was.
	pos, escAt int64
	// param is the number being read in a DEC private sequence, CSI ?, and
	// pending the tracked modes among the ones already read, as bits of
	// trackedModes: which way they go is only known at the end.
	param   int
	pending uint16
	modes   termModes
}

// push ends a control sequence parameter, noting it if it is a tracked mode.
// A sequence can switch any number of modes at once, and only the tracked ones
// are kept.
func (b *bellScanner) push() {
	for i, t := range trackedModes {
		if t.mode == b.param {
			b.pending |= 1 << i
			break
		}
	}
	b.param = 0
}

// control acts on a control character met inside a control sequence, which a
// terminal does where it stands: a bell rings, and an escape or a CAN or SUB
// abandons the sequence. i is where it is in the current chunk.
func (b *bellScanner) control(c byte, i int) (bell bool) {
	switch c {
	case 0x07:
		return true
	case 0x1b:
		b.state = scanEsc
		b.escAt = b.pos + int64(i)
	case 0x18, 0x1a:
		b.state = scanNormal
	}
	return false
}

// trackedModes are the terminal modes a replay has to put back, alternate
// screens first so that what follows is drawn on the screen it was drawn on.
// All but the cursor start switched off.
var trackedModes = [...]struct {
	mode        int
	onByDefault bool
}{
	// The alternate screens.
	{1049, false}, {1047, false}, {47, false},
	// Application cursor keys, and whether the cursor is shown.
	{1, false}, {25, true},
	// Mouse reporting, and the encodings it is reported in.
	{1000, false}, {1002, false}, {1003, false},
	{1005, false}, {1006, false}, {1015, false},
	// Focus reports and bracketed paste.
	{1004, false}, {2004, false},
}

// termModes is what a pane's output has done to the terminal's modes: which of
// trackedModes it has left other than their default, and where in the output
// each was last switched.
//
// A mode is switched once and holds until it is switched back, which for an
// agent's mouse reporting, vim's alternate screen or a shell's bracketed paste
// is usually the moment the program started. Once that has scrolled out of the
// history the replay no longer says so, and a window reloading an agent that
// has been running for an hour gets a terminal with all of them off: the wheel
// scrolls the window instead of the agent, a full-screen program draws over
// the scrollback, and a paste arrives as if it were typed.
type termModes struct {
	changed uint16
	at      [len(trackedModes)]int64
}

// apply records the modes in bits, as bits of trackedModes, switched on or
// off by the sequence beginning at offset at.
func (m *termModes) apply(bits uint16, on bool, at int64) {
	for i, t := range trackedModes {
		if bits&(1<<i) == 0 {
			continue
		}
		if on != t.onByDefault {
			m.changed |= 1 << i
		} else {
			m.changed &^= 1 << i
		}
		m.at[i] = at
	}
}

// restore returns the sequences that put back what the output left changed,
// for a replay beginning at offset from. A mode last switched at or after that
// is switched again by the replay itself.
func (m termModes) restore(from int64) []byte {
	var out []byte
	for i, t := range trackedModes {
		if m.changed&(1<<i) == 0 || m.at[i] >= from {
			continue
		}
		out = append(out, "\x1b[?"...)
		out = strconv.AppendInt(out, int64(t.mode), 10)
		if t.onByDefault {
			out = append(out, 'l')
		} else {
			out = append(out, 'h')
		}
	}
	return out
}

type scanState int

const (
	scanNormal scanState = iota
	scanEsc
	scanOSC
	scanOSCEsc
	scanCSI
	scanEscArg
	// scanCSIEntry is the first byte of a control sequence, which says whether
	// it is a private one, and scanCSIPrivate the rest of one that is.
	scanCSIEntry
	scanCSIPrivate
)

// scan reports whether p contains a real bell.
//
// It looks at every byte rather than skipping to the next escape or bell with
// bytes.IndexByte. That was tried: it is forty times faster on plain lines and
// two and a half times slower on the output a Claude pane actually produces,
// where an escape sequence every few bytes turns each search into call
// overhead over a span too short to pay for it. BenchmarkBellScan keeps all
// three shapes of output in view so the next attempt starts from the numbers.
func (b *bellScanner) scan(p []byte) bool {
	rang := false
	for i, c := range p {
		switch b.state {
		case scanNormal:
			switch c {
			case 0x1b:
				b.state = scanEsc
				b.escAt = b.pos + int64(i)
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
			case '[':
				b.state = scanCSIEntry
			case 'c':
				// A full reset puts every mode back as it started.
				b.modes = termModes{}
				b.state = scanNormal
			case 0x1b:
				// A second escape restarts the sequence rather than being the
				// body of the first.
				b.escAt = b.pos + int64(i)
			default:
				// Any other escape sequence is terminated by a byte that
				// cannot be BEL, so tracking it adds nothing.
				b.state = scanNormal
			}
		case scanCSIEntry, scanCSI:
			// A control sequence is read for the modes it switches, which only
			// a private one -- CSI ? -- can. The others, colours and cursor
			// moves and nearly everything else, are only watched for their end.
			switch {
			case c >= 0x40 && c <= 0x7e:
				b.state = scanNormal
			case c < 0x20:
				rang = b.control(c, i) || rang
			case c == '?' && b.state == scanCSIEntry:
				b.state = scanCSIPrivate
				b.param, b.pending = 0, 0
			default:
				b.state = scanCSI
			}
		case scanCSIPrivate:
			switch {
			case c >= '0' && c <= '9':
				b.param = min(b.param*10+int(c-'0'), 1<<20)
			case c == ';':
				b.push()
			case c >= 0x40 && c <= 0x7e:
				if c == 'h' || c == 'l' {
					b.push()
					b.modes.apply(b.pending, c == 'h', b.escAt)
				}
				b.state = scanNormal
			case c < 0x20:
				rang = b.control(c, i) || rang
			}
		case scanOSC:
			switch c {
			case 0x07:
				b.state = scanNormal // terminates the OSC; not a bell
			case 0x1b:
				b.state = scanOSCEsc
			}
		case scanOSCEsc:
			switch c {
			case '\\':
				b.state = scanNormal // ST terminator
			case 0x1b:
				// Still the start of a terminator, not the payload again.
				// Dropping back to the payload here leaves the scanner inside
				// the sequence for good, and the next real bell is swallowed
				// as the byte that ends it.
			default:
				b.state = scanOSC
			}
		}
	}
	b.pos += int64(len(p))
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
	// back moves the write position left, which is what the moves that rub
	// characters out amount to where text is appended rather than laid out.
	// It stops at the start of the line: a terminal's cursor does not carry
	// on into the line above, and letting it here would eat text that has
	// already been read as final.
	back := func(n int) {
		if n < 1 {
			n = 1
		}
		for ; n > 0; n-- {
			if len(out) <= lineStart {
				return
			}
			// A terminal moves by cells, not by bytes, and the spinners these
			// moves are used to draw are made of braille and box-drawing
			// characters three bytes wide. Taking one byte off the end of one
			// leaves a fragment of a character behind, which is not text at
			// all: the rest of the reading comes back as invalid UTF-8.
			i := len(out) - 1
			for i > lineStart && out[i]&0xc0 == 0x80 {
				i--
			}
			out = out[:i]
		}
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
			case c == 0x08:
				// A backspace is how a program takes back what it has just
				// printed -- a spinner frame, a character being erased --
				// and dropping it leaves both the character and the one that
				// replaced it in the text.
				back(1)
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
			// Moving up is how a program redraws what it drew last: a spinner
			// and the lines under it, a block of progress bars, a whole frame
			// of an Ink interface. What it moves back over is about to be
			// drawn again, so it is dropped rather than left standing above
			// the redraw -- where a question already answered went on being
			// read as the most recent thing the pane said.
			case c == 'A' || c == 'F':
				lineStart = lineAbove(out, lineStart, csiCount(params))
				out = out[:lineStart]
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
			// Moving it left is the other half of that. Handling only the
			// rightward move left the text of a redraw standing in front of
			// whatever redrew it.
			case c == 'D':
				back(csiCount(params))
			// Moving to a column is the other way of saying what a carriage
			// return says, and the way Claude Code's own interface says it:
			// go back to the start of the line and draw it again. Without
			// this the line before the redraw and the line after it are read
			// as one, which is how a status line that has counted to a
			// hundred arrives here as every number it passed through.
			case c == 'G':
				//
				// A column is a cell, not a byte. Counting bytes cut a spinner
				// frame or a box-drawing border in half, and the rest of the
				// reading came back as invalid UTF-8.
				col := csiCount(params)
				target := lineStart
				for ; col > 1; col-- {
					if target < len(out) {
						_, size := utf8.DecodeRune(out[target:])
						target += size
					} else {
						out = append(out, ' ')
						target++
					}
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
			// An escape here is the start of a terminator, not the payload
			// coming back. Treating it as the end of the sequence puts the
			// backslash that really ends it, and everything after it, into
			// the text: a window title arriving as prose.
			if c != 0x1b {
				state = scanNormal
			}
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
// current line. Horizontal moves and erase-in-line do not, and moving up is
// handled as the redraw it is.
func breaksLine(final byte) bool {
	switch final {
	case 'H', 'f', // cursor position
		'B', // down
		'E', // next line
		'J': // erase in display
		return true
	}
	return false
}

// lineAbove returns where the line n lines above the one beginning at start
// begins, stopping at the first line there is.
func lineAbove(out []byte, start, n int) int {
	for ; n > 0 && start > 0; n-- {
		start = bytes.LastIndexByte(out[:start-1], '\n') + 1
	}
	return start
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

// patternStatus reads out of a pane's recent output the state the agent's own
// lifecycle would have reported, for an agent that reports none. The second
// return value is false when nothing in it says either way, which leaves the
// coarser guesses -- the bell, the quiet timer -- to speak for the pane.
//
// It is called with s.mu held, from the reader, once per chunk a pane prints.
// An agent with no patterns is the common case and costs a length check.
func (s *Session) patternStatus() (Status, bool) {
	if len(s.patterns.Waiting) == 0 && len(s.patterns.Idle) == 0 {
		return StatusIdle, false
	}
	raw, truncated := s.history.tail(patternBytes)
	if truncated {
		raw = dropPartialLine(raw)
	}
	return matchPatterns(stripANSI(raw), s.patterns)
}

// matchPatterns finds the most recent line of output that says what an agent is
// doing.
//
// The most recent wins because the lines arrive in the order the agent printed
// them: one that asked a question and has since printed its prompt again is no
// longer waiting for an answer to it. The last line counts even with no newline
// after it, because the prompt an agent is sitting at is exactly the line it
// has not finished. The patterns are the ones foldPatterns returns.
func matchPatterns(text string, p agent.Patterns) (Status, bool) {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.ToLower(strings.TrimSpace(lines[i]))
		if line == "" {
			continue
		}
		// Waiting is asked first, so that a line answering to both is read as
		// the one worth surfacing.
		if containsAny(line, p.Waiting) {
			return StatusWaiting, true
		}
		if containsAny(line, p.Idle) {
			return StatusIdle, true
		}
	}
	return StatusIdle, false
}

// containsAny reports whether a lower-cased line holds any of the patterns,
// which foldPatterns has already lower-cased.
//
// The patterns are plain text rather than expressions. They come from a file
// the user may edit, and what a mistake in an expression there costs is paid by
// every pane: one that matches everything leaves a whole workspace reporting
// the same status, and one that backtracks is run over every chunk every pane
// prints. The lines worth recognising are fixed prose an agent wrote into
// itself, so a substring finds them, and case is ignored because whether a
// prompt is capitalised is not something a catalog entry should have to know.
func containsAny(lowered string, pats []string) bool {
	for _, pat := range pats {
		if strings.Contains(lowered, pat) {
			return true
		}
	}
	return false
}

// foldPatterns trims and lower-cases an agent's patterns once, when its pane
// starts, and drops any left empty. They are compared with every line of every
// chunk the pane prints, and folding them there cost an allocation per pattern
// per line: six times the cost of publishing a chunk at all.
func foldPatterns(p agent.Patterns) agent.Patterns {
	fold := func(pats []string) []string {
		var out []string
		for _, pat := range pats {
			if pat = strings.ToLower(strings.TrimSpace(pat)); pat != "" {
				out = append(out, pat)
			}
		}
		return out
	}
	return agent.Patterns{Waiting: fold(p.Waiting), Idle: fold(p.Idle)}
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
