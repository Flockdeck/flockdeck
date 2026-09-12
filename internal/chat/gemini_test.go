package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A Gemini model that thinks before calling a function signs the call, and the
// request carrying the answer is refused unless the signature comes back on it.
// Its reasoning is also billed as output while being counted apart from the
// answer.
func TestGeminiHandsBackACallsSignatureAndCountsItsThinking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"candidates":[{"content":{"parts":[` +
			`{"functionCall":{"name":"read_file","args":{"path":"a"}},"thoughtSignature":"sig-g"}]},"finishReason":"STOP"}],` +
			`"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":10,"thoughtsTokenCount":40}}` + "\n\n"))
	}))
	defer srv.Close()

	var calls []ToolCall
	var usage Usage
	w := &geminiWire{base: srv.URL}
	err := w.Stream(context.Background(), Request{Model: "gemini-3-pro"}, func(ev Event) {
		switch ev.Kind {
		case EventCall:
			calls = append(calls, ev.Call)
		case EventUsage:
			usage = ev.Usage
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Signature != "sig-g" {
		t.Fatalf("calls = %+v, want one signed sig-g", calls)
	}
	if usage.Out != 50 {
		t.Errorf("output = %d tokens, want the 10 of the answer and the 40 of thinking", usage.Out)
	}

	contents := geminiContents([]Message{
		{Role: RoleUser, Text: "read a"},
		{Role: RoleAssistant, Calls: calls},
		{Role: RoleTool, Call: calls[0], Text: "contents"},
	})
	data, _ := json.Marshal(contents[1])
	if !strings.Contains(string(data), `"thoughtSignature":"sig-g"`) {
		t.Errorf("the call went back without its signature: %s", data)
	}
}

func TestGeminiWithNoModelSaysSoRatherThanAskingForAPath(t *testing.T) {
	asked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = true
		http.NotFound(w, r)
	}))
	defer srv.Close()

	err := (&geminiWire{base: srv.URL}).Stream(context.Background(), Request{}, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "/model") {
		t.Errorf("error = %v, want one saying how to name a model", err)
	}
	if asked {
		t.Error("a request was sent to an address with no model in it")
	}
}
