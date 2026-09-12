package chat

import (
	"strings"
	"testing"
)

// A resumed conversation hands the model its earlier tool outputs as text, and
// each has to say what it came from: several "[read_file]" blocks in a row do
// not say which file each is.
func TestAResumedToolOutputSaysWhatItCameFrom(t *testing.T) {
	msgs := Messages([]Entry{
		{Type: "user", Text: "compare them"},
		{Type: "tool", Tool: "read_file", Call: "read_file a.go", Text: "package a"},
		{Type: "tool", Tool: "read_file", Text: "package b"},
	})
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", msgs)
	}
	for _, want := range []string{"[read_file a.go]\npackage a", "[read_file]\npackage b"} {
		if !strings.Contains(msgs[0].Text, want) {
			t.Errorf("the conversation lacks %q:\n%s", want, msgs[0].Text)
		}
	}
}
