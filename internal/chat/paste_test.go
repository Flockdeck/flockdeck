package chat

import (
	"context"
	"io"
	"testing"
	"time"
)

// A paste arrives as a burst of lines, and sent a line at a time a pasted stack
// trace became as many prompts as it had lines. Lines arriving together at a
// terminal are one prompt; lines typed one after another are not.
func TestAPasteIsOnePrompt(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	s, _ := newTestSession(t, "", nil)
	s.in = newInput(r, true)
	ctx := context.Background()

	go w.Write([]byte("why does this fail?\npanic: boom\n\tat main.go:12\n"))
	line, ok := s.readPrompt(ctx)
	if want := "why does this fail?\npanic: boom\n\tat main.go:12"; !ok || line != want {
		t.Fatalf("prompt = %q, want the whole paste %q", line, want)
	}

	go func() {
		w.Write([]byte("first\n"))
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("second\n"))
	}()
	if line, _ := s.readPrompt(ctx); line != "first" {
		t.Errorf("first typed prompt = %q", line)
	}
	if line, _ := s.readPrompt(ctx); line != "second" {
		t.Errorf("second typed prompt = %q, want it on its own", line)
	}
}
