package chat

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

// In a narrow pane the line of answers under a question would be broken
// mid-word by the terminal, in the one place the user is reading to choose.
// There each answer gets a line of its own; where it fits, it stays one line.
func TestTheAnswersFitTheWidthOfThePane(t *testing.T) {
	tool := &alwaysTool{fakeTool: fakeTool{name: "write_file", question: "write a.txt?", answer: "ok"}}
	tool.always = "edits to files"

	s, out := newTestSession(t, "n\n", nil, tool)
	s.opts.Width = 40
	s.runCalls(context.Background(), []ToolCall{{ID: "c1", Name: "write_file"}})
	for _, line := range strings.Split(out.String(), "\n") {
		if utf8.RuneCountInString(line) > 40 {
			t.Errorf("a line of %d columns in a pane of 40: %q", utf8.RuneCountInString(line), line)
		}
	}
	if !strings.Contains(out.String(), "  [a] always for `edits to files`\n") {
		t.Errorf("the answers were not drawn a line each:\n%s", out.String())
	}

	s, out = newTestSession(t, "n\n", nil, tool)
	s.opts.Width = 200
	s.runCalls(context.Background(), []ToolCall{{ID: "c1", Name: "write_file"}})
	if !strings.Contains(out.String(), "[y] yes, once   [a] always") {
		t.Errorf("a wide pane did not get the answers on one line:\n%s", out.String())
	}
}
