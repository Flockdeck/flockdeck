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

// An endpoint that offers exactly one model answers with it, without anybody
// having to choose it with /model first.
func TestTheOneModelAnEndpointOffersIsTaken(t *testing.T) {
	var mu sync.Mutex
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Write([]byte(`{"data":[{"id":"qwen2.5-coder:7b"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			asked = string(body)
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
		}
	}))
	defer srv.Close()

	var out strings.Builder
	err := Run(context.Background(), Options{
		Agent: "local", Wire: "openai", BaseURL: srv.URL + "/v1",
		Session: "s", Dir: t.TempDir(), In: strings.NewReader("hello\n/exit\n"), Out: &out, Width: 90,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "(answering with qwen2.5-coder:7b, the one model the endpoint offers)") ||
		strings.Contains(out.String(), "no model is named") {
		t.Errorf("the one model was not taken:\n%s", out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(asked, `"model":"qwen2.5-coder:7b"`) {
		t.Errorf("the request did not name the model: %s", asked)
	}
}
