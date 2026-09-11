package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// failingTool is a tool that refuses, the way a tool does for a path outside
// the pane or an edit that does not match.
type failingTool struct{ name string }

func (f failingTool) Name() string                    { return f.name }
func (f failingTool) Describe() Schema                { return Schema{} }
func (f failingTool) Approval(json.RawMessage) string { return "" }
func (f failingTool) Run(context.Context, json.RawMessage) (string, error) {
	return "", errors.New("path is outside the pane's working directory")
}

// The model is told why a call failed; the person watching it try again has
// to be told as well, or the retry makes no sense to them.
func TestAFailedToolCallIsShownAsWellAsAnswered(t *testing.T) {
	s, out := newTestSession(t, "", nil, failingTool{name: "read_file"})
	calls := []ToolCall{{ID: "c1", Name: "read_file"}, {ID: "c2", Name: "no_such_tool"}}
	s.runCalls(context.Background(), calls)
	for _, want := range []string{"failed: path is outside", "no tool called no_such_tool"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the pane does not show %q:\n%s", want, out.String())
		}
	}
}

// A request's tool calls are not the stream's, and have no index: a server
// strict about its schema refuses a field it does not know.
func TestOpenAIRequestsCarryNoStreamIndexOnCalls(t *testing.T) {
	msgs := openaiMessages("", []Message{
		{Role: RoleUser, Text: "look"},
		{Role: RoleAssistant, Calls: []ToolCall{{ID: "a", Name: "x"}, {ID: "b", Name: "y"}}},
	})
	data, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"index"`) {
		t.Errorf("the request carries a stream index: %s", data)
	}
}
