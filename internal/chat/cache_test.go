package chat

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every request sends the whole conversation again, and a tool loop sends it
// again at every step. The request marks the system prompt and the end of the
// conversation for the cache, so that what the model was just told is read
// back at a tenth of the price rather than paid for in full each time.
func TestAnthropicMarksTheConversationForTheCache(t *testing.T) {
	var sent anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&sent)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()
	w := &anthropicWire{base: srv.URL}
	if err := w.Stream(context.Background(), Request{Model: "m", System: "be brief", Messages: conversation}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if len(sent.System) != 1 || sent.System[0].CacheControl == nil {
		t.Errorf("the system prompt is not marked for the cache: %+v", sent.System)
	}
	marks := 0
	for i, m := range sent.Messages {
		for j, b := range m.Content {
			if b.CacheControl != nil {
				marks++
				if i != len(sent.Messages)-1 || j != len(m.Content)-1 {
					t.Errorf("a mark in the middle of the conversation, at message %d block %d", i, j)
				}
			}
		}
	}
	if marks != 1 {
		t.Errorf("%d marks in the conversation, want one at its end", marks)
	}
}

func TestCachedInputIsCountedAndPricedAsSuch(t *testing.T) {
	got := streamAnthropic(t,
		`{"type":"message_start","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":5000,"cache_creation_input_tokens":200,"output_tokens":1}}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":40}}`,
		`{"type":"message_stop"}`,
	)
	want := Usage{In: 5300, Out: 40, CacheRead: 5000, CacheWrite: 200}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	// Opus 5 at $5 in and $25 out: 100 fresh at 5, 5000 read at 0.5, 200
	// written at 6.25 and 40 out at 25, per million.
	c, ok := cost("claude-opus-5", got)
	if !ok || math.Abs(c-5250.0/1e6) > 1e-12 {
		t.Errorf("cost = %v, want %v", c, 5250.0/1e6)
	}
	if line := statusLine("claude-opus-5", got, spend{}); !strings.Contains(line, "5.3k in (5.0k cached)") {
		t.Errorf("status line %q does not say what was cached", line)
	}
}
