package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

// emitOne sends one hook payload through the whole transport and returns the
// event the server received.
func emitOne(t *testing.T, event, payload string) Event {
	t.Helper()
	srv, r := newServer(t)
	if _, err := Emit(strings.NewReader(payload), srv.Endpoint(), srv.Token(), "pane-1", event); err != nil {
		t.Fatalf("emit: %v", err)
	}
	return r.next(t)
}

func TestDetailCarriesWhatTheRecorderNeeds(t *testing.T) {
	got := emitOne(t, "PostToolUse", `{"session_id":"s","tool_name":"Grep","tool_use_id":"toolu_1",
		"tool_input":{"pattern":"TODO","path":"internal"},"tool_response":{"matches":3}}`)
	d := got.Detail
	if d == nil || d.ToolUseID != "toolu_1" {
		t.Fatalf("detail = %+v", d)
	}
	var in map[string]any
	if err := json.Unmarshal(d.Input, &in); err != nil || in["pattern"] != "TODO" {
		t.Errorf("a tool the status code ignores lost its input: %s (%v)", d.Input, err)
	}
	if d.Result != `{"matches":3}` {
		t.Errorf("result = %q", d.Result)
	}

	if got := emitOne(t, "Stop", `{"session_id":"s","last_assistant_message":"Done."}`); got.Detail == nil || got.Detail.Message != "Done." {
		t.Errorf("the assistant's last message was not carried: %+v", got.Detail)
	}
	if got := emitOne(t, "PostToolUseFailure", `{"session_id":"s","tool_name":"Bash","error":"exit 2"}`); got.Detail == nil || got.Detail.Error != "exit 2" {
		t.Errorf("a failure's error was not carried: %+v", got.Detail)
	}
	if got := emitOne(t, "UserPromptSubmit", `{"session_id":"s","prompt":"`+strings.Repeat("a", 10<<10)+`"}`); got.Detail == nil || len(got.Detail.Prompt) < 10<<10 {
		t.Errorf("a prompt over the tab-naming cap was cut for the recorder too")
	}
	// The interrupted tool keeps its detail under its own event name.
	if got := emitOne(t, "PostToolUseFailure", `{"session_id":"s","tool_name":"Bash","is_interrupt":true,"error":"stopped"}`); got.Event != Interrupted || got.Detail == nil || got.Detail.Error != "stopped" {
		t.Errorf("interrupted = %q, %+v", got.Event, got.Detail)
	}
	// An event with nothing for the recorder carries no detail at all.
	if got := emitOne(t, "SessionEnd", `{"session_id":"s"}`); got.Detail != nil {
		t.Errorf("SessionEnd carries %+v", got.Detail)
	}
}

func TestDetailIsBounded(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	in, _ := json.Marshal(map[string]any{"session_id": "s", "tool_name": "Write", "tool_use_id": "t",
		"tool_input":    map[string]any{"file_path": "a.txt", "content": big},
		"tool_response": big})
	got := emitOne(t, "PostToolUse", string(in))
	d := got.Detail
	if d == nil {
		t.Fatal("no detail")
	}
	if len(d.Input) > maxDetailInput+64 || len(d.Result) > maxDetailString+64 {
		t.Errorf("detail is unbounded: input %d, result %d", len(d.Input), len(d.Result))
	}
	if !strings.Contains(d.Result, "[clipped ") {
		t.Errorf("a clipped result does not say so")
	}
	if !json.Valid(d.Input) {
		t.Errorf("clipping broke the input's JSON")
	}
}
