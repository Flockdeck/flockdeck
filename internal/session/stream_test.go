package session

import (
	"bytes"
	"strings"
	"testing"
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
		{"carriage returns", "first\r\nsecond\r\n", "first\nsecond\n"},
		{"bell", "ding\x07dong", "dingdong"},
		// A charset designator carries the set it selects in the byte after
		// the escape; leaving that behind puts a stray letter in the prose.
		{"charset designator", "\x1b(Bplain ascii", "plain ascii"},
		{"alternate charset", "\x1b)0line\x1b(Btext", "linetext"},
		{"line size", "\x1b#8grid", "grid"},
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

// TestTailLinesDropsThePartialFirstLine covers the byte cut the tail of a
// pane's output is taken at. Landing inside an escape sequence spills its
// parameters into the text as if they were words, which is what the fan-out
// dialog then shows to the user.
func TestTailLinesDropsThePartialFirstLine(t *testing.T) {
	raw := []byte("\x1b[38;5;42mearlier line\x1b[m\r\n\x1b[1mlater line\x1b[m\r\n")

	// A budget that lands part way through the first line's colour sequence.
	got := stripANSI(tailLines(raw, 41))
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
		if got := stripANSI(tailLines(raw, n)); !strings.Contains(got, "earlier line") {
			t.Errorf("tailLines(_, %d) dropped output that fitted: %q", n, got)
		}
	}

	// Output with no line break at all is better shown truncated than lost.
	if got := tailLines([]byte("no breaks here"), 5); string(got) != " here" {
		t.Errorf("unbroken output = %q, want the tail", got)
	}
}
