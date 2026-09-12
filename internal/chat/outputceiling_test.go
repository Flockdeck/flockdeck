package chat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const anthropicHi = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

// An older model refuses the default ceiling on an answer and says what it
// will take; the request is made again with that, rather than every /retry
// being refused the same way. A ceiling the user asked for is left alone.
func TestAModelThatTakesLessOutputIsAskedWithItsOwnCeiling(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		if strings.Contains(string(body), `"max_tokens":8192`) {
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, anthropicHi)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: 32000 > 8192, which is the maximum allowed number of output tokens for claude-3-5-haiku-20241022"}}`)
	}))
	defer srv.Close()

	wire, _ := NewWire("anthropic", srv.URL, "k")
	req := Request{Model: "claude-3-5-haiku-20241022", Messages: []Message{{Role: RoleUser, Text: "hello"}}}
	var said strings.Builder
	err := wire.Stream(context.Background(), req, func(ev Event) {
		if ev.Kind == EventText {
			said.WriteString(ev.Text)
		}
	})
	if err != nil || said.String() != "hi" {
		t.Fatalf("Stream = %v, said %q", err, said.String())
	}
	mu.Lock()
	if len(bodies) != 2 || !strings.Contains(bodies[1], `"max_tokens":8192`) {
		t.Errorf("requests = %q", bodies)
	}
	bodies = nil
	mu.Unlock()

	// A ceiling the user set is theirs, and its refusal is said, not worked round.
	req.MaxTokens = 32000
	if err := wire.Stream(context.Background(), req, func(Event) {}); err == nil {
		t.Error("a ceiling the user asked for was quietly lowered")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 {
		t.Errorf("made %d requests for a ceiling the user asked for, want 1", len(bodies))
	}
}
