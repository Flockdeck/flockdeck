package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A refusal is said the same way on every wire, and does not send anybody to
// /retry: asking the same again is refused the same way.
func TestARefusalIsSaidAlikeAndNotOfferedARetry(t *testing.T) {
	for _, c := range []struct{ name, wire, body string }{
		{"anthropic refusal", "anthropic", strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":1}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``, ``,
		}, "\n")},
		{"openai content filter", "openai", `data: {"choices":[{"delta":{},"finish_reason":"content_filter"}]}` + "\n\ndata: [DONE]\n\n"},
		{"gemini safety", "gemini", `data: {"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}` + "\n\n"},
		{"gemini blocked prompt", "gemini", `data: {"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}` + "\n\n"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(c.body))
		}))
		wire, err := NewWire(c.wire, srv.URL, "k")
		if err != nil {
			t.Fatal(err)
		}
		out := run(t, Options{Agent: c.wire, Model: "m"}, "hello\n/exit\n", wire)
		srv.Close()
		said := strings.Join(strings.Fields(out), " ")
		if !strings.Contains(said, "the request was refused:") || !strings.Contains(said, "asking the same again is refused the same way") ||
			strings.Contains(said, "/retry") {
			t.Errorf("%s: the refusal was not said as one:\n%s", c.name, out)
		}
	}
}
