package chat

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// A line typed while the model was answering is drawn after the prompt it is
// sent from, rather than being sent from a prompt that shows nothing.
func TestALineTypedDuringAnAnswerIsShownAtThePrompt(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	s, out := newTestSession(t, "", nil)
	s.in = newInput(r, true)

	// Typed while the model was still answering: waiting before the prompt.
	go w.Write([]byte("and the second question\n"))
	time.Sleep(50 * time.Millisecond)

	line, ok := s.readPrompt(context.Background())
	if !ok || line != "and the second question" {
		t.Fatalf("readPrompt = %q, %v", line, ok)
	}
	if !strings.Contains(out.String(), "you > and the second question") {
		t.Errorf("the line was not shown at the prompt:\n%q", out.String())
	}
}
