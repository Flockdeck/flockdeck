package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// streamAnthropic answers one request with the given data lines and returns
// the usage the wire reported.
func streamAnthropic(t *testing.T, lines ...string) Usage {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			w.Write([]byte("data: " + l + "\n\n"))
		}
	}))
	defer srv.Close()
	var usage Usage
	w := &anthropicWire{base: srv.URL}
	if err := w.Stream(context.Background(), Request{Model: "m", MaxTokens: 10}, func(ev Event) {
		if ev.Kind == EventUsage {
			usage = ev.Usage
		}
	}); err != nil {
		t.Fatal(err)
	}
	return usage
}

// The counts in message_delta are the whole answer's so far, and the server
// repeats the input count there; the status line and the cost were built from
// their sum with message_start's, which read every prompt twice.
func TestAnthropicUsageIsNotCountedTwice(t *testing.T) {
	got := streamAnthropic(t,
		`{"type":"message_start","message":{"usage":{"input_tokens":1200,"output_tokens":1}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1200,"output_tokens":40}}`,
		`{"type":"message_stop"}`,
	)
	if got.In != 1200 || got.Out != 40 {
		t.Errorf("usage = %+v, want 1200 in and 40 out", got)
	}
	if !strings.Contains(statusLine("m", got), "1.2k in") {
		t.Errorf("status line = %q", statusLine("m", got))
	}
}
