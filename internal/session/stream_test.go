package session

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRingKeepsMostRecentBytes(t *testing.T) {
	r := newRing(8)

	if got := r.bytes(); len(got) != 0 {
		t.Errorf("new ring should be empty, got %q", got)
	}

	r.write([]byte("abc"))
	if got := string(r.bytes()); got != "abc" {
		t.Errorf("after short write: %q, want abc", got)
	}

	// Fill exactly.
	r.write([]byte("defgh"))
	if got := string(r.bytes()); got != "abcdefgh" {
		t.Errorf("when exactly full: %q, want abcdefgh", got)
	}

	// Overflow discards the oldest.
	r.write([]byte("ij"))
	if got := string(r.bytes()); got != "cdefghij" {
		t.Errorf("after overflow: %q, want cdefghij", got)
	}

	// A single write larger than the whole buffer keeps only its tail.
	r.write([]byte("0123456789"))
	if got := string(r.bytes()); got != "23456789" {
		t.Errorf("after oversized write: %q, want 23456789", got)
	}
}

func TestRingWrapsRepeatedly(t *testing.T) {
	r := newRing(16)
	for i := 0; i < 100; i++ {
		r.write([]byte("xy"))
	}
	got := r.bytes()
	if len(got) != 16 {
		t.Fatalf("ring length = %d, want 16", len(got))
	}
	if !bytes.Equal(got, []byte(strings.Repeat("xy", 8))) {
		t.Errorf("ring contents = %q", got)
	}
}

// TestBellScannerIgnoresOSCTerminator is the point of having a scanner at all.
// Claude Code sets the window title with an OSC sequence terminated by BEL, so
// searching the stream for 0x07 would report a bell on every title change and
// mark every pane as needing attention.
func TestBellScannerIgnoresOSCTerminator(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain bell", "hi\x07there", true},
		{"osc title terminated by BEL", "\x1b]0;my title\x07rest", false},
		{"osc title terminated by ST", "\x1b]0;my title\x1b\\rest", false},
		{"bell after an osc title", "\x1b]0;t\x07ding\x07", true},
		{"csi sequence", "\x1b[1;31mred\x1b[m", false},
		{"osc containing an escape that is not ST", "\x1b]8;;http://x\x1b]y\x07", false},
		// DCS, APC and PM carry a payload the same way OSC does, and the BEL
		// that ends one is no more a bell than the BEL that ends a title.
		{"dcs terminated by BEL", "\x1bPtmux;data\x07after", false},
		{"apc terminated by BEL", "\x1b_G f=100\x07after", false},
		{"pm terminated by BEL", "\x1b^message\x07after", false},
		{"a doubled escape still starts the sequence", "\x1b\x1b]0;t\x07", false},
		// An escape inside the payload that is not the terminator is still the
		// start of one: losing that leaves the scanner inside the sequence,
		// and the next real bell is swallowed as the byte that ends it.
		{"an escape before the ST terminator", "\x1b]0;t\x1b\x1b" + `\` + "ding\x07", true},
		{"no bell at all", "just text", false},
	}
	for _, c := range cases {
		var b bellScanner
		if got := b.scan([]byte(c.in)); got != c.want {
			t.Errorf("%s: scan(%q) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

// TestBellScannerAcrossChunks checks the state survives a sequence split across
// reads, which is exactly how it arrives from a PTY.
func TestBellScannerAcrossChunks(t *testing.T) {
	var b bellScanner
	// An OSC title split mid-sequence; the trailing BEL still terminates it.
	if b.scan([]byte("\x1b]0;par")) {
		t.Error("no bell should be reported inside an OSC sequence")
	}
	if b.scan([]byte("tial title\x07")) {
		t.Error("the BEL terminating a split OSC sequence is not a bell")
	}
	if !b.scan([]byte("\x07")) {
		t.Error("a bell after the OSC ended should be reported")
	}
}

// TestStripANSIRecoversText covers reading a pane's output as prose, which is
// what the fan-out dialog looks at for a plan.
func TestStripANSIRecoversText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"colour", "\x1b[38;5;42mgreen\x1b[m text", "green text"},
		// A terminal application moves the cursor instead of printing newlines,
		// so those moves have to stand in as line breaks or separate lines run
		// together.
		{"cursor moves break lines", "a\x1b[2Ab\x1b[1;5Hc", "a\nb\nc"},
		// A horizontal move does not break the line: it is how a full-screen
		// program draws blanks, so it has to come back as the blanks it stood
		// for. Losing them is how "the words" arrives as "thewords".
		{"horizontal moves become blanks", "ab\x1b[3Ccd", "ab   cd"},
		{"an unparameterised move is one blank", "ab\x1b[Ccd", "ab cd"},
		{"a runaway move is capped", "a\x1b[99999Cb", "a" + strings.Repeat(" ", maxCSICount) + "b"},
		{"osc title", "\x1b]0;window title\x07visible", "visible"},
		{"osc with ST", "\x1b]8;;http://x\x1b\\link", "link"},
		// An escape inside the payload is the start of a terminator, not the
		// payload coming back: reading it as the end of the sequence puts the
		// backslash that really ends it, and the rest of the title, on screen.
		{"osc with a doubled escape before ST", "\x1b]0;window title\x1b\x1b\\visible", "visible"},
		{"carriage returns", "first\r\nsecond\r\n", "first\nsecond\n"},
		// On its own a carriage return rewinds to the start of the line and
		// what follows overwrites it, which is how a progress line redraws.
		{"a redrawn line keeps only its last state", "50%\r100%\ndone", "100%\ndone"},
		{"repeated redraws", "a\rb\rc", "c"},
		{"a redraw after a real line break", "first\nhalf\rwhole", "first\nwhole"},
		{"bell", "ding\x07dong", "dingdong"},
		// A charset designator carries the set it selects in the byte after
		// the escape; leaving that behind puts a stray letter in the prose.
		{"charset designator", "\x1b(Bplain ascii", "plain ascii"},
		{"alternate charset", "\x1b)0line\x1b(Btext", "linetext"},
		{"line size", "\x1b#8grid", "grid"},
		// Moving to a column is the other spelling of a carriage return, and
		// the one Claude Code's own interface uses to redraw a line.
		{"a column move rewinds the line", "50%\x1b[1G100%", "100%"},
		{"an unparameterised column move is column one", "50%\x1b[G100%", "100%"},
		{"a column move keeps what is before it", "abcdef\x1b[3Gxy", "abxy"},
		{"a column move past the end pads", "ab\x1b[5Gcd", "ab  cd"},
		{"a column move after a line break stays on its line", "first\n50%\x1b[1G100%", "first\n100%"},
		// Erasing the whole line is the rest of that idiom.
		{"erasing the line drops it", "stale text\x1b[2K\x1b[1Gfresh", "fresh"},
		{"erasing to the end of the line drops nothing", "kept\x1b[K", "kept"},
		// Moving the write position back is how a program takes back what it
		// has just printed. Dropping the move leaves both what was printed and
		// what replaced it.
		{"backspace rubs out the character before it", "abcx\bd", "abcd"},
		{"backspace stops at the start of its line", "a\nb\b\bc", "a\nc"},
		{"cursor left moves back over what it wrote", "100%\x1b[4D 50%", " 50%"},
		{"cursor left stops at the start of its line", "ab\x1b[9Dcd", "cd"},
		{"an unparameterised cursor left is one place", "abx\x1b[Dy", "aby"},
		{"plain", "nothing to strip", "nothing to strip"},
	}
	for _, c := range cases {
		if got := stripANSI([]byte(c.in)); got != c.want {
			t.Errorf("%s: stripANSI(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestRecentTextReadsTheTail checks a pane's output can be read back as text.
func TestRecentTextReadsTheTail(t *testing.T) {
	s := startShell(t)
	s.publish([]byte("\x1b[32m- first task\x1b[m\r\n"))
	s.publish([]byte("- second task\r\n"))

	got := s.RecentText(0)
	if !strings.Contains(got, "- first task") || !strings.Contains(got, "- second task") {
		t.Errorf("recent text lost the content: %q", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Error("escape sequences should not survive")
	}
}

// TestRecentTextDropsThePartialFirstLine covers the byte cut the tail of a
// pane's output is taken at. Landing inside an escape sequence spills its
// parameters into the text as if they were words, which is what the fan-out
// dialog then shows to the user.
func TestRecentTextDropsThePartialFirstLine(t *testing.T) {
	raw := []byte("\x1b[38;5;42mearlier line\x1b[m\r\n\x1b[1mlater line\x1b[m\r\n")
	pane := func() *Session {
		s := &Session{history: newRing(1024)}
		s.history.write(raw)
		return s
	}

	// A budget that lands part way through the first line's colour sequence.
	got := pane().RecentText(41)
	if strings.Contains(got, "5;42") {
		t.Errorf("escape parameters leaked into the text: %q", got)
	}
	if strings.Contains(got, "earlier") {
		t.Errorf("the cut line should be dropped whole: %q", got)
	}
	if !strings.Contains(got, "later line") {
		t.Errorf("the last line should survive: %q", got)
	}

	// A budget bigger than the output keeps all of it, and so does no budget.
	for _, n := range []int{0, -1, len(raw), len(raw) * 2} {
		if got := pane().RecentText(n); !strings.Contains(got, "earlier line") {
			t.Errorf("RecentText(%d) dropped output that fitted: %q", n, got)
		}
	}

	// Output with no line break at all is better shown truncated than lost.
	s := &Session{history: newRing(1024)}
	s.history.write([]byte("no breaks here"))
	if got := s.RecentText(5); got != " here" {
		t.Errorf("unbroken output = %q, want the tail", got)
	}
}

// TestRingTailMatchesBytes checks the cheap tail against the whole-buffer read
// it replaces, across every wrap position a ring can be in.
func TestRingTailMatchesBytes(t *testing.T) {
	for _, size := range []int{1, 4, 7, 16} {
		for written := 0; written < size*3; written++ {
			r := newRing(size)
			for i := 0; i < written; i++ {
				r.write([]byte{byte('a' + i%26)})
			}
			all := r.bytes()
			for n := -1; n <= size+2; n++ {
				got, truncated := r.tail(n)
				want := all
				if n > 0 && n < len(all) {
					want = all[len(all)-n:]
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("size %d, %d written, tail(%d) = %q, want %q", size, written, n, got, want)
				}
				if truncated != (len(got) < len(all)) {
					t.Fatalf("size %d, %d written, tail(%d) reported truncated = %v", size, written, n, truncated)
				}
			}
		}
	}
}

// BenchmarkRecentText measures reading the tail of a pane's output, which the
// fan-out dialog does for every open pane at once.
func BenchmarkRecentText(b *testing.B) {
	s := &Session{history: newRing(replayBytes)}
	line := []byte(strings.Repeat("x", 79) + "\n")
	for s.history.pos != 0 || !s.history.full {
		s.history.write(line)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(s.RecentText(64<<10)) == 0 {
			b.Fatal("no text")
		}
	}
}

// TestReplayStartsAtALineBoundary covers reloading a pane that has said more
// than its buffer holds. The oldest byte kept is wherever the last write
// landed, and a terminal handed the tail of an escape sequence has nothing to
// attach the parameters to and prints them as if they were words.
func TestReplayStartsAtALineBoundary(t *testing.T) {
	// Sized so the buffer wraps six bytes into the colour sequence on the
	// second line, which is the middle of its parameters.
	s := &Session{history: newRing(24), subs: map[int]chan []byte{}, idleAfter: time.Minute}
	s.publish([]byte("first line\n"))
	s.publish([]byte("\x1b[38;5;42mgreen\x1b[m\n"))
	s.publish([]byte("plain tail\n"))

	id, replay, _ := s.Subscribe()
	t.Cleanup(func() { s.Unsubscribe(id) })

	if strings.Contains(string(replay), "42m") {
		t.Errorf("the replay opens inside an escape sequence: %q", replay)
	}
	if !strings.Contains(string(replay), "plain tail") {
		t.Errorf("the replay lost the whole lines it did hold: %q", replay)
	}

	// A buffer that has not wrapped is replayed whole: there is no partial
	// line at the front of it, and dropping one would lose the first thing the
	// pane ever said.
	fresh := &Session{history: newRing(4096), subs: map[int]chan []byte{}, idleAfter: time.Minute}
	fresh.publish([]byte("the first line\nthe second\n"))
	id2, replay2, _ := fresh.Subscribe()
	t.Cleanup(func() { fresh.Unsubscribe(id2) })
	if !strings.Contains(string(replay2), "the first line") {
		t.Errorf("replay dropped the start of a buffer that never wrapped: %q", replay2)
	}
}

// BenchmarkBellScan measures the scan every byte of every pane's output goes
// through on the way from the process to the screen, over the three shapes
// terminal output comes in.
func BenchmarkBellScan(b *testing.B) {
	shapes := []struct{ name, unit string }{
		// A build log, or anything writing plain lines.
		{"plain", "some ordinary output on a line of its own\n"},
		// Coloured output: a sequence every few words.
		{"coloured", "\x1b[38;5;42m" + "some ordinary output " + "\x1b[m\r\n"},
		// A full-screen interface redrawing itself, which is what a Claude
		// pane produces: escape sequences with a few characters between them.
		{"full screen", "\x1b[K\x1b[36mx\x1b[m\x1b[1B"},
	}
	for _, shape := range shapes {
		b.Run(shape.name, func(b *testing.B) {
			chunk := []byte(strings.Repeat(shape.unit, (32<<10)/len(shape.unit)))
			var s bellScanner
			b.SetBytes(int64(len(chunk)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.scan(chunk)
			}
		})
	}
}
