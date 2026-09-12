package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// streamFrom answers one request on the named wire with body, and returns the
// events and the error the wire ended with.
func streamFrom(t *testing.T, wire, body string) ([]Event, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(body))
	}))
	defer srv.Close()
	wr, err := NewWire(wire, srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	err = wr.Stream(context.Background(), Request{Model: "m"}, func(ev Event) { events = append(events, ev) })
	return events, err
}

// An answer cut off at the model's limit, or stopped by a filter, says why in
// the stream; read as finished, it is half a reply shown as the whole, and a
// call cut off mid-argument would run with its arguments missing.
func TestAnAnswerStoppedShortSaysWhyOnEveryWire(t *testing.T) {
	tests := []struct {
		name, wire, body, want string
	}{
		{"openai length", "openai",
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"write_file","arguments":"{\"path\":"}}]}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n" + "data: [DONE]\n\n",
			"limit"},
		{"openai filter", "openai",
			`data: {"choices":[{"delta":{"content":"par"},"finish_reason":"content_filter"}]}` + "\n\n" + "data: [DONE]\n\n",
			"content filter"},
		{"gemini max tokens", "gemini",
			`data: {"candidates":[{"content":{"parts":[{"text":"par"}]},"finishReason":"MAX_TOKENS"}]}` + "\n\n",
			"limit"},
		{"gemini safety", "gemini",
			`data: {"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}` + "\n\n",
			"SAFETY"},
		{"gemini blocked prompt", "gemini",
			`data: {"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}` + "\n\n",
			"refused the prompt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			events, err := streamFrom(t, tc.wire, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one mentioning %q", err, tc.want)
			}
			for _, ev := range events {
				if ev.Kind == EventCall {
					t.Errorf("a call was handed on to be run: %+v", ev.Call)
				}
			}
		})
	}
}

func TestAnAnswerThatFinishedNormallyIsNotAnError(t *testing.T) {
	for _, c := range []struct{ wire, body string }{
		{"openai", `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}` + "\n\n" + "data: [DONE]\n\n"},
		{"openai", `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"x","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n" + "data: [DONE]\n\n"},
		{"gemini", `data: {"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}` + "\n\n"},
	} {
		if _, err := streamFrom(t, c.wire, c.body); err != nil {
			t.Errorf("%s: %v", c.wire, err)
		}
	}
}
