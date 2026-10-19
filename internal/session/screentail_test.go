package session

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLastInputIsZeroUntilSomeoneTypes(t *testing.T) {
	s := fakeSession(newFakePTY())
	if got := s.LastInput(); !got.IsZero() {
		t.Fatalf("LastInput on a new pane = %v, want the zero time", got)
	}
}

func TestLastInputIgnoresATerminalsOwnReports(t *testing.T) {
	s := fakeSession(newFakePTY())
	_, _ = s.Write([]byte("\x1b[I")) // focus gained
	_, _ = s.Write([]byte("\x1b[O")) // focus lost
	if got := s.LastInput(); !got.IsZero() {
		t.Fatalf("a focus report set LastInput to %v", got)
	}
}

// Each way text reaches a pane ends in Write: the window's input path and a
// remote device call it with the bytes they were sent, and Flockdeck's own
// typeInto goes through WriteString.
func TestLastInputUpdatesForEachWriteSource(t *testing.T) {
	sources := []struct {
		name string
		send func(s *Session)
	}{
		{"window or remote bytes", func(s *Session) { _, _ = s.Write([]byte("ls\r")) }},
		{"typeInto text", func(s *Session) { _ = s.WriteString("hello") }},
		{"typeInto enter", func(s *Session) { _ = s.WriteString("\r") }},
		{"bracketed paste", func(s *Session) { _ = s.WriteString("\x1b[200~pasted\x1b[201~") }},
	}
	for _, src := range sources {
		t.Run(src.name, func(t *testing.T) {
			s := fakeSession(newFakePTY())
			before := time.Now()
			src.send(s)
			got := s.LastInput()
			if got.Before(before) || got.After(time.Now()) {
				t.Fatalf("LastInput = %v, want a time between %v and now", got, before)
			}
		})
	}
}

func TestLastInputMovesForwardOnEachWrite(t *testing.T) {
	s := fakeSession(newFakePTY())
	_ = s.WriteString("a")
	first := s.LastInput()
	time.Sleep(15 * time.Millisecond)
	_ = s.WriteString("b")
	if second := s.LastInput(); !second.After(first) {
		t.Fatalf("second write left LastInput at %v, first was %v", second, first)
	}
}

func TestScreenTailOfAPaneThatPrintedNothing(t *testing.T) {
	s := fakeSession(newFakePTY())
	tail := s.ScreenTail()
	if len(tail.Lines) != 0 || !tail.ChangedAt.IsZero() {
		t.Fatalf("empty pane tail = %+v, want no lines and the zero time", tail)
	}
}

func TestScreenTailStripsEscapesAndKeepsTheLastLines(t *testing.T) {
	s := fakeSession(newFakePTY())
	var out strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&out, "\x1b[32mline %d\x1b[0m   \r\n", i)
	}
	out.WriteString("> ")
	s.publish([]byte(out.String()))

	tail := s.ScreenTail()
	if len(tail.Lines) != ScreenTailLines {
		t.Fatalf("got %d lines, want %d: %q", len(tail.Lines), ScreenTailLines, tail.Lines)
	}
	if first := tail.Lines[0]; first != "line 10" {
		t.Errorf("first line = %q, want \"line 10\"", first)
	}
	if last := tail.Lines[len(tail.Lines)-1]; last != ">" {
		t.Errorf("last line = %q, want the prompt with trailing space trimmed", last)
	}
}

func TestScreenTailDropsTrailingBlankLines(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.publish([]byte("done\n\n\n"))
	tail := s.ScreenTail()
	if got := strings.Join(tail.Lines, "|"); got != "done" {
		t.Fatalf("tail = %q, want %q", got, "done")
	}
}

func TestScreenTailChangedAtResetsOnNewOutput(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.publish([]byte("one\n"))
	first := s.ScreenTail().ChangedAt
	if first.IsZero() {
		t.Fatal("ChangedAt is zero after output")
	}
	time.Sleep(15 * time.Millisecond)
	s.publish([]byte("two\n"))
	second := s.ScreenTail().ChangedAt
	if !second.After(first) {
		t.Fatalf("new output left ChangedAt at %v, was %v", second, first)
	}
}

// A prompt redrawn over itself is output, and moves the pane's last-output
// time, but it leaves the text where it was. That is the case the wake
// service needs to see through: an idle agent that repaints its status line
// every second is still idle.
func TestScreenTailChangedAtIgnoresARedrawOfTheSameText(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.publish([]byte("done\n> "))
	first := s.ScreenTail()

	time.Sleep(15 * time.Millisecond)
	s.publish([]byte("\r\x1b[2K> "))
	s.publish([]byte("\r\x1b[2K> "))
	second := s.ScreenTail()

	if !second.ChangedAt.Equal(first.ChangedAt) {
		t.Fatalf("a redraw of the same text moved ChangedAt from %v to %v", first.ChangedAt, second.ChangedAt)
	}
	if got := strings.Join(second.Lines, "|"); got != "done|>" {
		t.Fatalf("tail after redraw = %q, want %q", got, "done|>")
	}

	time.Sleep(15 * time.Millisecond)
	s.publish([]byte("x"))
	if third := s.ScreenTail(); !third.ChangedAt.After(first.ChangedAt) {
		t.Fatalf("typed text left ChangedAt at %v, was %v", third.ChangedAt, first.ChangedAt)
	}
}

// Output that arrived before the first call is dated by its last chunk, which
// is the latest it could have changed, and not by the time of the call.
func TestScreenTailFirstCallDatesEarlierOutputByItsLastChunk(t *testing.T) {
	s := fakeSession(newFakePTY())
	s.publish([]byte("early\n"))
	arrived := time.Now()
	time.Sleep(20 * time.Millisecond)

	got := s.ScreenTail().ChangedAt
	if got.After(arrived) {
		t.Fatalf("ChangedAt = %v is later than the output at about %v", got, arrived)
	}
}

func TestScreenTailAndLastInputAreSafeFromAnyGoroutine(t *testing.T) {
	s := fakeSession(newFakePTY())
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				switch n % 4 {
				case 0:
					s.publish([]byte("output line\r\n"))
				case 1:
					_ = s.WriteString("x")
				case 2:
					_ = s.ScreenTail()
				default:
					_ = s.LastInput()
				}
			}
		}(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// BenchmarkPublishTailUnwatched and BenchmarkPublishTailWatched compare the
// cost of one chunk before anyone has asked for the tail and after. The
// first is the cost every pane pays; it must match BenchmarkPublish.
func BenchmarkPublishTailUnwatched(b *testing.B) {
	benchmarkPublishTail(b, false)
}

func BenchmarkPublishTailWatched(b *testing.B) {
	benchmarkPublishTail(b, true)
}

func benchmarkPublishTail(b *testing.B, watched bool) {
	s := fakeSession(newFakePTY())
	s.idleAfter = time.Minute
	s.OnChange = func() {}
	if watched {
		_ = s.ScreenTail()
	}
	chunk := []byte(strings.Repeat("some output text\r\n", 8))
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.publish(chunk)
	}
}
