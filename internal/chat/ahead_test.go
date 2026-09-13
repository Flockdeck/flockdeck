package chat

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
)

// promptWatch is a session's output that says when a question's prompt has
// been drawn. The question is on the screen once "  > " is written, and only
// then is the session waiting for a line to answer it with.
type promptWatch struct {
	mu     sync.Mutex
	b      strings.Builder
	once   sync.Once
	prompt chan struct{}
}

func newPromptWatch() *promptWatch { return &promptWatch{prompt: make(chan struct{})} }

func (p *promptWatch) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.b.Write(b)
	if strings.Contains(p.b.String(), "  > ") {
		p.once.Do(func() { close(p.prompt) })
	}
	return len(b), nil
}

// A line typed while the model was working is the next prompt, not an answer to
// a question that was not on the screen when it was typed. It is kept for the
// prompt, and the question waits for a line typed after it.
func TestALineTypedAheadIsNotTakenAsAnAnswer(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	write := &fakeTool{name: "write_file", question: "write a.txt?", answer: "wrote"}
	s, _ := newTestSession(t, "", nil, write)
	s.in = newInput(r, true)
	watch := newPromptWatch()
	s.out = newPrinter(watch, 70, false)

	// Typed while the model was still answering. A pipe's write returns once
	// the reader has taken the bytes, so the line is waiting before the
	// question is asked, as a line the terminal had already delivered is.
	if _, err := w.Write([]byte("and then run the tests\n")); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runCalls(context.Background(), []ToolCall{{ID: "c1", Name: "write_file"}})
	}()
	<-watch.prompt
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
