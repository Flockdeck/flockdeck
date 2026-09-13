package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// openaiStream streams body from an httptest server through the OpenAI wire,
// and returns the calls and the reasoning it gave.
func openaiStream(t *testing.T, body string) (calls []ToolCall, thinking string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(body))
	}))
	defer srv.Close()
	var thought strings.Builder
	err := (&openaiWire{base: srv.URL}).Stream(context.Background(), Request{Model: "m"}, func(ev Event) {
		switch ev.Kind {
		case EventCall:
			calls = append(calls, ev.Call)
		case EventThinking:
			thought.WriteString(ev.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return calls, thought.String()
}

// A server that leaves the index off its tool calls, but gives each call its
// own id, is asking for two calls. Read by index alone, both were one call
// with the second's name and both sets of arguments run together.
func TestOpenAICallsWithoutAnIndexAreToldApartByTheirIDs(t *testing.T) {
	calls, _ := openaiStream(t, strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"call-a","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"\"a.txt\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"call-b","function":{"name":"list_dir","arguments":"{\"path\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"\"src\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n"))
	if len(calls) != 2 {
		t.Fatalf("calls = %+v, want two", calls)
	}
	for i, want := range []struct{ id, name, path string }{{"call-a", "read_file", "a.txt"}, {"call-b", "list_dir", "src"}} {
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(calls[i].Args, &args); err != nil || calls[i].ID != want.id || calls[i].Name != want.name || args.Path != want.path {
			t.Errorf("call %d = %+v (%s), want %s %s on %s", i, calls[i], calls[i].Args, want.id, want.name, want.path)
		}
	}
}

// The servers that follow OpenRouter's lead send a reasoning model's thinking
// as "reasoning" rather than "reasoning_content", and it is shown all the same.
func TestOpenAIReasoningIsReadUnderEitherName(t *testing.T) {
	_, thinking := openaiStream(t, strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning":"thinking it over"}}]}`,
		`data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n"))
	if thinking != "thinking it over" {
		t.Errorf("reasoning = %q, want what was sent as reasoning", thinking)
	}
}
