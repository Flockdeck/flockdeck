package chat

import (
	"io"
	"testing"
	"time"
)

// bumpyReader is a terminal whose input ends once and then carries on, which is
// what a Windows console does when Ctrl+C arrives during a read. An empty chunk
// is one end of input; after the last chunk the input has really ended.
type bumpyReader struct{ chunks []string }

func (b *bumpyReader) Read(p []byte) (int, error) {
	if len(b.chunks) == 0 {
		return 0, io.EOF
	}
	c := b.chunks[0]
	b.chunks = b.chunks[1:]
	if c == "" {
		return 0, io.EOF
	}
	return copy(p, c), nil
}

func nextLineOf(t *testing.T, in *input) (string, bool) {
	t.Helper()
	select {
	case l := <-in.lines:
		return l, true
	case <-in.closed:
		return "", false
	case <-time.After(3 * time.Second):
		t.Fatal("no line and no end of input")
		return "", false
	}
}

func TestCtrlCOnAConsoleDoesNotEndTheInput(t *testing.T) {
	in := newInput(&bumpyReader{chunks: []string{"one\n", "", "two\n"}}, true)
	if l, ok := nextLineOf(t, in); !ok || l != "one" {
		t.Fatalf("first line = %q, %v", l, ok)
	}
	in.interrupt()
	if l, ok := nextLineOf(t, in); !ok || l != "two" {
		t.Fatalf("after Ctrl+C the input ended rather than reading %q (%q, %v)", "two", l, ok)
	}
	// The input really ends after that, and one Ctrl+C does not excuse it.
	if _, ok := nextLineOf(t, in); ok {
		t.Error("a line appeared from nowhere")
	}
}

func TestTheEndOfInputIsStillTheEnd(t *testing.T) {
	for _, console := range []bool{true, false} {
		in := newInput(&bumpyReader{chunks: []string{"one\n", "", "two\n"}}, console)
		nextLineOf(t, in)
		if l, ok := nextLineOf(t, in); ok {
			t.Errorf("console=%v: read %q past the end of the input with no Ctrl+C", console, l)
		}
	}
}
