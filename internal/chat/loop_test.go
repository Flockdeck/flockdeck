package chat

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// newTestSession builds a session the way Run does, without the parts of Run
// that decide when a turn starts, so that one piece of the loop can be driven
// and then looked at.
func newTestSession(t *testing.T, input string, wire Wire, tools ...Tool) (*session, *strings.Builder) {
	t.Helper()
	log, err := OpenLog(t.TempDir(), "session-1", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	var out strings.Builder
	s := &session{
		opts:     Options{Width: 70, Tools: tools},
		wire:     wire,
		log:      log,
		out:      newPrinter(&out, 70, false),
		in:       newInput(strings.NewReader(input), false),
		reporter: newReporter("", "", "session-1", ""),
		tools:    map[string]Tool{},
		always:   map[string]bool{},
		signals:  make(chan os.Signal),
	}
	for _, tool := range tools {
		s.tools[tool.Name()] = tool
	}
	return s, &out
}

// unanswered lists the calls in a conversation that no tool message answers,
// which every wire rejects the whole request over.
func unanswered(msgs []Message) []string {
	answered := map[string]bool{}
	for _, m := range msgs {
		if m.Role == RoleTool {
			answered[m.Call.ID] = true
		}
	}
	var out []string
	for _, m := range msgs {
		for _, c := range m.Calls {
			if !answered[c.ID] {
				out = append(out, c.ID)
			}
		}
	}
	return out
}

func TestEveryCallIsAnsweredWhenTheUserNeverAnswers(t *testing.T) {
	ask := &fakeTool{name: "write_file", question: "write?", answer: "wrote"}
	read := &fakeTool{name: "read_file", answer: "contents"}
	s, _ := newTestSession(t, "", nil, ask, read)
	calls := []ToolCall{
		{ID: "c1", Name: "write_file", Args: json.RawMessage(`{}`)},
		{ID: "c2", Name: "read_file", Args: json.RawMessage(`{}`)},
	}
	s.messages = []Message{{Role: RoleUser, Text: "go"}, {Role: RoleAssistant, Calls: calls}}

	if stop := s.runCalls(context.Background(), calls); !stop {
		t.Error("the turn carried on with nobody to answer the question")
	}
	if got := unanswered(s.messages); len(got) > 0 {
		t.Errorf("calls %v were left without an answer", got)
	}
	if read.ran() != 0 {
		t.Error("a call after the unanswered question ran anyway")
	}
}

func TestNoToolRunsOnceTheTurnIsInterrupted(t *testing.T) {
	read := &fakeTool{name: "read_file", answer: "contents"}
	s, _ := newTestSession(t, "", nil, read)
	calls := []ToolCall{{ID: "c1", Name: "read_file"}, {ID: "c2", Name: "read_file"}}
	s.messages = []Message{{Role: RoleUser, Text: "go"}, {Role: RoleAssistant, Calls: calls}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if stop := s.runCalls(ctx, calls); !stop {
		t.Error("an interrupted turn went back to the model")
	}
	if read.ran() != 0 {
		t.Errorf("the tool ran %d times after the interrupt", read.ran())
	}
	if got := unanswered(s.messages); len(got) > 0 {
		t.Errorf("calls %v were left without an answer", got)
	}
}

func TestATurnStoppedAtItsLimitLeavesNoCallUnanswered(t *testing.T) {
	var turns []turnFunc
	for i := 0; i < maxToolSteps+5; i++ {
		turns = append(turns, asksFor(ToolCall{ID: "c" + strings.Repeat("x", i), Name: "list_dir"}))
	}
	tool := &fakeTool{name: "list_dir", answer: "a b c"}
	s, _ := newTestSession(t, "", &scriptedWire{turns: turns}, tool)

	s.turn(context.Background(), "look around")
	if got := unanswered(s.messages); len(got) > 0 {
		t.Errorf("calls %v were left without an answer, so the next turn would be refused", got)
	}
}
