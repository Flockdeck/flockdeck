package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A model that thinks before it calls a tool has to be shown that thinking
// again, unchanged, in the request that carries the call's answer -- or the
// request is refused. The current models think by default and send their
// reasoning with nothing in it but a signature, which is the case here.
func TestAnthropicHandsBackTheReasoningBehindACall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, data := range []string{
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a\"}"}}`,
			`{"type":"message_stop"}`,
		} {
			w.Write([]byte("data: " + data + "\n\n"))
		}
	}))
	defer srv.Close()

	var got []Thinking
	var calls []ToolCall
	w := &anthropicWire{base: srv.URL}
	err := w.Stream(context.Background(), Request{Model: "claude-sonnet-5", MaxTokens: 100}, func(ev Event) {
		switch ev.Kind {
		case EventReasoning:
			got = append(got, ev.Thinking)
		case EventCall:
			calls = append(calls, ev.Call)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Signature != "sig-abc" {
		t.Fatalf("reasoning = %+v, want one block signed sig-abc", got)
	}

	msgs := anthropicMessages([]Message{
		{Role: RoleUser, Text: "read a"},
		{Role: RoleAssistant, Calls: calls, Thinking: got},
		{Role: RoleTool, Call: calls[0], Text: "contents"},
	})
	data, err := json.Marshal(msgs[1].Content)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"thinking","thinking":"","signature":"sig-abc"},{"type":"tool_use","id":"toolu_1","name":"read_file","input":{"path":"a"}}]`
	if string(data) != want {
		t.Errorf("the assistant turn went back as\n%s\nwant\n%s", data, want)
	}
}

func TestTheLoopKeepsReasoningWithTheCallItLedTo(t *testing.T) {
	wire := &scriptedWire{turns: []turnFunc{
		func(_ context.Context, _ Request, emit func(Event)) error {
			emit(Event{Kind: EventReasoning, Thinking: Thinking{Signature: "sig"}})
			emit(Event{Kind: EventCall, Call: ToolCall{ID: "c1", Name: "list_dir"}})
			return nil
		},
		says("done"),
	}}
	tool := &fakeTool{name: "list_dir", answer: "a b"}
	run(t, Options{Agent: "anthropic", Tools: []Tool{tool}, Task: "look"}, "", wire)

	reqs := wire.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2", len(reqs))
	}
	var found bool
	for _, m := range reqs[1].Messages {
		if m.Role == RoleAssistant && len(m.Calls) == 1 {
			found = len(m.Thinking) == 1 && m.Thinking[0].Signature == "sig"
		}
	}
	if !found {
		t.Errorf("the reasoning was not handed back with its call: %+v", reqs[1].Messages)
	}
}
