package chat

import (
	"context"
	"io"
	"testing"
	"time"
)

// A line typed while the model was working is the next prompt, not an answer to
// a question that was not on the screen when it was typed. It is kept for the
// prompt, and the question waits for a line typed after it.
func TestALineTypedAheadIsNotTakenAsAnAnswer(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	write := &fakeTool{name: "write_file", question: "write a.txt?", answer: "wrote"}
	s, _ := newTestSession(t, "", nil, write)
	s.in = newInput(r, true)

	// Typed while the model was still answering.
	go w.Write([]byte("and then run the tests\n"))
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runCalls(context.Background(), []ToolCall{{ID: "c1", Name: "write_file"}})
	}()
	time.Sleep(100 * time.Millisecond)
	w.Write([]byte("y\n")) // the answer, typed once the question was up
	<-done

	if write.ran() != 1 {
		t.Errorf("the tool ran %d times; the answer given after the question was %q", write.ran(), "y")
	}
	if len(s.ahead) != 1 || s.ahead[0] != "and then run the tests" {
		t.Fatalf("kept %q, want the line typed ahead", s.ahead)
	}
	line, ok := s.readPrompt(context.Background())
	if !ok || line != "and then run the tests" {
		t.Errorf("the next prompt was %q, %v; want the line typed ahead", line, ok)
	}
}

// Several lines typed ahead of a question -- a paste, most often -- are one
// prompt when the prompt comes, as they would have been had they arrived at
// it, rather than a turn each.
func TestLinesTypedAheadAreOnePrompt(t *testing.T) {
	s, _ := newTestSession(t, "", nil)
	s.ahead = []string{"panic: boom", "", "goroutine 1 [running]:"}
	line, ok := s.readPrompt(context.Background())
	if !ok || line != "panic: boom\n\ngoroutine 1 [running]:" {
		t.Errorf("the next prompt was %q, %v; want all three lines", line, ok)
	}
	if len(s.ahead) != 0 {
		t.Errorf("left %q for the prompt after", s.ahead)
	}
}
