package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Saying no usually comes with what to do instead. Whatever the user types that
// is not one of the letters declines the call and reaches the model as their
// instruction, rather than being refused as a bad answer and asked again.
func TestAReplyThatIsNotALetterDeclinesAndIsPassedOn(t *testing.T) {
	write := &fakeTool{name: "write_file", question: "write a.txt?", answer: "wrote"}
	read := &fakeTool{name: "read_file", answer: "contents"}
	s, out := newTestSession(t, "put it in b.txt instead\n", nil, write, read)
	calls := []ToolCall{
		{ID: "c1", Name: "write_file", Args: json.RawMessage(`{}`)},
		{ID: "c2", Name: "read_file", Args: json.RawMessage(`{}`)},
	}
	s.messages = []Message{{Role: RoleUser, Text: "go"}, {Role: RoleAssistant, Calls: calls}}

	if stop := s.runCalls(context.Background(), calls); stop {
		t.Error("the turn stopped instead of taking the instruction back to the model")
	}
	if write.ran() != 0 || read.ran() != 0 {
		t.Errorf("tools ran after the user said to do something else: write %d, read %d", write.ran(), read.ran())
	}
	var answer string
	for _, m := range s.messages {
		if m.Role == RoleTool && m.Call.ID == "c1" {
			answer = m.Text
		}
	}
	if !strings.Contains(answer, "put it in b.txt instead") {
		t.Errorf("the model was told %q, want the user's own words", answer)
	}
	if got := unanswered(s.messages); len(got) > 0 {
		t.Errorf("calls %v were left without an answer", got)
	}
	if strings.Contains(out.String(), "answer y or n") {
		t.Errorf("the reply was refused and asked again:\n%s", out.String())
	}
}

func TestTheQuestionSaysEveryAnswerItTakes(t *testing.T) {
	tool := &alwaysTool{fakeTool: fakeTool{name: "run_command", question: "run `go test`?", answer: "ok"}}
	tool.always = "go test"
	s, out := newTestSession(t, "a\n", nil, tool)
	s.runCalls(context.Background(), []ToolCall{{ID: "c1", Name: "run_command"}})
	for _, want := range []string{"[y]", "[a] always for `go test`", "[n] no (Enter)", "what to do instead"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the question does not offer %q:\n%s", want, out.String())
		}
	}
	if tool.ran() != 1 {
		t.Errorf("the tool ran %d times after [a]", tool.ran())
	}
}
