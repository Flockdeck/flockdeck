package chat

import (
	"encoding/json"
	"strings"
	"testing"
)

// Scrolling back through /history, a tool's line has to say what it acted on:
// "read_file  package main" says what came back but not from which file.
func TestHistorySaysWhatEachToolActedOn(t *testing.T) {
	tool := &fakeTool{name: "read_file", answer: "1\tpackage main\n2\t\n"}
	wire := &scriptedWire{turns: []turnFunc{
		asksFor(ToolCall{ID: "c1", Name: "read_file", Args: json.RawMessage(`{"path":"src/main.go"}`)}),
		says("it is empty"),
	}}
	out := run(t, Options{Agent: "anthropic", Tools: []Tool{tool}, Task: "what is in main.go?"}, "/history\n/exit\n", wire)

	_, history, found := strings.Cut(out, "it is empty")
	if !found {
		t.Fatalf("the turn did not finish:\n%s", out)
	}
	if !strings.Contains(history, "· read_file src/main.go") {
		t.Errorf("/history does not say which file was read:\n%s", history)
	}
}
