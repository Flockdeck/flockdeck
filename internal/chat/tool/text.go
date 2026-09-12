package tool

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// The file tools' dealings with text: how much of it a question shows, how its
// lines are split, counted and compared, and what a size or a count reads as.

// excerptLines is how much of a write the question shows.
const excerptLines = 6

// excerpt is up to excerptLines lines of s from line from on.
func excerpt(s string, from int) string {
	lines := textLines(s)
	if from < 1 || from > len(lines) {
		return ""
	}
	return marked(lines[from-1:], "│")
}

// textLines splits s into lines without inventing an empty last one for the
// newline that ends it.
func textLines(s string) []string {
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
}

// marked draws up to excerptLines of lines, each on a line of its own behind
// mark and cut to a width a pane shows, with a count of the rest.
func marked(lines []string, mark string) string {
	var b strings.Builder
	shown := lines
	if len(shown) > excerptLines {
		shown = shown[:excerptLines]
	}
	for _, l := range shown {
		r := []rune(l)
		if len(r) > 100 {
			r = append(r[:100], '…')
		}
		fmt.Fprintf(&b, "  %s %s\n", mark, string(r))
	}
	if rest := len(lines) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "  %s … %d more %s", mark, rest, plural(rest, "line", "lines"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// firstChange is the line at which b first differs from a, or 0 when the two
// are the same.
func firstChange(a, b string) int {
	if a == b {
		return 0
	}
	al := strings.Split(strings.ReplaceAll(a, "\r\n", "\n"), "\n")
	bl := strings.Split(strings.ReplaceAll(b, "\r\n", "\n"), "\n")
	for i := range bl {
		if i >= len(al) || al[i] != bl[i] {
			return i + 1
		}
	}
	// b is a's opening lines and no more: what changed is that the rest went.
	return len(bl)
}

// looseMatch is the line at which want appears in file once differences of
// spacing are set aside, or 0 where it does not. Lines are compared with their
// runs of spaces and tabs made one space; the first line of want may be the
// end of a line in the file and its last line the start of one, as they are
// when an edit begins or ends part-way through a line.
func looseMatch(file, want string) int {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	wl, fl := textLines(want), textLines(file)
	for i := 0; i+len(wl) <= len(fl); i++ {
		ok := true
		for j, w := range wl {
			line, w := norm(fl[i+j]), norm(w)
			switch {
			case len(wl) == 1:
				ok = strings.Contains(line, w)
			case j == 0:
				ok = strings.HasSuffix(line, w)
			case j == len(wl)-1:
				ok = strings.HasPrefix(line, w)
			default:
				ok = line == w
			}
			if !ok {
				break
			}
		}
		if ok {
			return i + 1
		}
	}
	return 0
}

// withCRLF writes every line ending in s as a carriage return and a newline.
func withCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// looksBinary reports whether data is something a model should be shown as
// text. A NUL byte near the start is the cheap, and in practice reliable,
// signal: no source file has one and almost every binary format does.
func looksBinary(data []byte) bool {
	head := data
	if len(head) > 8<<10 {
		head = head[:8<<10]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// nextLine reads one line without its ending, keeping at most max bytes of it
// and reporting how many were dropped. A file's last line may have no newline,
// in which case it comes back with io.EOF.
//
// A line has a ceiling of its own because one line of a minified bundle can be
// megabytes long, and handing it over whole would spend the context window on
// one read however few lines were asked for.
func nextLine(r *bufio.Reader, max int) (string, int, error) {
	var kept []byte
	dropped := 0
	for {
		chunk, err := r.ReadSlice('\n')
		if err != bufio.ErrBufferFull {
			// The line's own ending is not text: it is neither kept nor
			// counted among what the reader did not see.
			chunk = bytes.TrimRight(chunk, "\r\n")
		}
		if room := max - len(kept); len(chunk) > room {
			kept, dropped = append(kept, chunk[:room]...), dropped+len(chunk)-room
		} else {
			kept = append(kept, chunk...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if dropped == 0 {
			// A CRLF split across two reads leaves its carriage return here.
			kept = bytes.TrimRight(kept, "\r")
		}
		// Cutting by byte count can land in the middle of a rune.
		return strings.ToValidUTF8(string(kept), ""), dropped, err
	}
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

func linesOf(s string) string {
	n := countLines(s)
	return fmt.Sprintf("%d %s", n, plural(n, "line", "lines"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// humanBytes is a size as it should be read aloud, not as it is stored.
func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f kB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
