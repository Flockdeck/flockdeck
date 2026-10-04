package helpers

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadLinesCutsLongLinesAndAlwaysDrains(t *testing.T) {
	pr, pw := io.Pipe()
	written := make(chan struct{})
	go func() {
		defer close(written)
		defer pw.Close()
		_, _ = io.WriteString(pw, "first\n")
		_, _ = io.WriteString(pw, strings.Repeat("x", 2<<20)+"\n")
		for i := 0; i < 1000; i++ {
			_, _ = io.WriteString(pw, "ordinary line\n")
		}
		_, _ = io.WriteString(pw, "no newline at the end")
	}()
	var lines []string
	readLines(pr, func(l string) { lines = append(lines, l) })
	select {
	case <-written:
	case <-time.After(10 * time.Second):
		t.Fatal("the writer was left blocked: the reader gave up before the end")
	}
	if len(lines) != 1003 {
		t.Fatalf("%d lines", len(lines))
	}
	if lines[0] != "first" || lines[1002] != "no newline at the end" {
		t.Fatalf("first %q, last %q", lines[0], lines[1002])
	}
	if len(lines[1]) > maxLogLine+len(" [cut off]") || !strings.HasSuffix(lines[1], "[cut off]") {
		t.Fatalf("the long line is %d bytes: %.20q", len(lines[1]), lines[1])
	}
	if lines[2] != "ordinary line" {
		t.Fatalf("the line after the long one is %q", lines[2])
	}
}

func TestReadLinesEdges(t *testing.T) {
	var got []string
	readLines(strings.NewReader("a\r\n\nb"), func(l string) { got = append(got, l) })
	if strings.Join(got, "|") != "a||b" {
		t.Fatalf("got %q", got)
	}
	got = nil
	readLines(strings.NewReader(""), func(l string) { got = append(got, l) })
	if len(got) != 0 {
		t.Fatalf("got %q", got)
	}
	// Exactly at the limit is not cut.
	got = nil
	readLines(strings.NewReader(strings.Repeat("z", maxLogLine)+"\n"), func(l string) { got = append(got, l) })
	if len(got) != 1 || len(got[0]) != maxLogLine {
		t.Fatalf("got %d lines, first %d bytes", len(got), len(got[0]))
	}
}

func TestPrintableRemovesControlCharacters(t *testing.T) {
	in := "a\x1b[31mred\x1b[0m\x07bell\x1b]0;title\x07\ttab\r\x7f\u0085end"
	out := printable(in)
	for _, r := range out {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			t.Fatalf("a control character survived: %q", out)
		}
	}
	if !strings.Contains(out, "red") || !strings.Contains(out, "end") {
		t.Fatalf("the text was lost: %q", out)
	}
	if got := ringLine(strings.Repeat("a", 5000)); len(got) > maxRingLine+len(" [cut off]") {
		t.Fatalf("a ring line is %d bytes", len(got))
	}
}
