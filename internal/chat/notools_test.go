package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A local model served without tool support refuses every request that offers
// tools. It is talked to without them rather than not at all, the user is told
// once, and the tools are not offered to it again.
func TestAModelWithoutToolsIsTalkedToWithoutThem(t *testing.T) {
	var offered []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		_, has := body["tools"]
		offered = append(offered, has)
		if has {
			http.Error(w, `{"error":{"message":"registry.ollama.ai/library/gemma:2b does not support tools"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	wire := &openaiWire{base: srv.URL}
	req := Request{Model: "gemma:2b", Tools: []Tool{echoTool{name: "read_file"}}}
	notices := 0
	count := func(ev Event) {
		if ev.Kind == EventNotice {
			notices++
		}
	}
	for i := 0; i < 2; i++ {
		if err := wire.Stream(context.Background(), req, count); err != nil {
			t.Fatalf("turn %d: %v", i+1, err)
		}
	}
	if len(offered) != 3 || !offered[0] || offered[1] || offered[2] {
		t.Errorf("tools offered %v, want once, then never again", offered)
	}
	if notices != 1 {
		t.Errorf("told the user %d times, want once", notices)
	}
}
