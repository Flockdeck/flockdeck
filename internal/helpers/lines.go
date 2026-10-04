package helpers

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Output from a helper is untrusted: it can be one line of any length, and it
// can hold control characters. Neither may cost Flockdeck memory or a stalled
// pipe, so lines are cut short and the pipe is always read to its end.

const (
	// maxLogLine is how much of one line goes to the log file.
	maxLogLine = 2048
	// maxRingLine is how much of one line is kept for the status, which is
	// broadcast to every window and printed by the CLI.
	maxRingLine = 512
)

// readLines calls f for each line of r, with at most maxLogLine bytes of it and
// a note when the rest was cut off. It never stops reading before r ends or
// fails: a reader that gave up on a line that was too long left the helper
// blocked writing to a pipe nobody emptied.
func readLines(r io.Reader, f func(line string)) {
	br := bufio.NewReaderSize(r, 8192)
	var cur []byte
	cut := false
	flush := func() {
		line := string(cur)
		if cut {
			line += " [cut off]"
		}
		f(line)
		cur, cut = cur[:0], false
	}
	for {
		frag, more, err := br.ReadLine()
		if room := maxLogLine - len(cur); room > 0 {
			cur = append(cur, frag[:min(room, len(frag))]...)
			if len(frag) > room {
				cut = true
			}
		} else if len(frag) > 0 {
			cut = true
		}
		if err != nil {
			if len(cur) > 0 || cut {
				flush()
			}
			_, _ = io.Copy(io.Discard, br)
			return
		}
		if !more {
			flush()
		}
	}
}

// printable replaces control characters, which would let a helper's output
// move a terminal's cursor, set its title or hide text from the person reading
// it, with a visible mark. Tabs become spaces.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return '?'
		}
		return r
	}, s)
}

// Printable is printable for callers outside the package.
func Printable(s string) string { return printable(s) }

// ringLine is a line as it is kept for the status.
func ringLine(s string) string {
	s = printable(s)
	if len(s) > maxRingLine {
		s = s[:maxRingLine] + " [cut off]"
	}
	return s
}

// probeClient asks a helper's own port whether it is ready and healthy. It
// goes nowhere but where it is told (no proxy, whatever the environment
// says), follows no redirect (an answer that sends the probe elsewhere is not
// a 200 from the helper), and gives up quickly.
var probeClient = &http.Client{
	Timeout: 2 * time.Second,
	Transport: &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext:       (&net.Dialer{Timeout: time.Second}).DialContext,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}
