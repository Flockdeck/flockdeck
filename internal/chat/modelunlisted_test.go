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

// The models a server on this machine lists at the start are kept, so part of
// a name finds one of several without /model having been run first.
func TestPartOfANameFindsAModelListedAtTheStart(t *testing.T) {
	var mu sync.Mutex
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Write([]byte(`{"data":[{"id":"qwen2.5-coder:7b"},{"id":"llama3.2"}]}`))
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
		Session: "s", Dir: t.TempDir(), In: strings.NewReader("/model qwen\nhello\n/exit\n"), Out: &out, Width: 90,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "answering with qwen2.5-coder:7b from here on") {
		t.Errorf("/model qwen did not find the model:\n%s", out.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(asked, `"model":"qwen2.5-coder:7b"`) {
		t.Errorf("the request named %s", asked)
	}
}

// listingWire is an endpoint that can say which models it has.
type listingWire struct {
	scriptedWire
	models []string
}

func (w *listingWire) ListModels(context.Context) ([]string, error) { return w.models, nil }

// With nothing listed yet, /model with part of a name asks the endpoint first,
// rather than sending the part as the id itself.
func TestPartOfANameAsksTheEndpointWhenNothingIsListed(t *testing.T) {
	wire := &listingWire{models: []string{"qwen2.5-coder:7b", "llama3.2"}}
	s, out := newTestSession(t, "", wire)
	s.chooseModel(context.Background(), "qwen")
	if s.model != "qwen2.5-coder:7b" {
		t.Errorf("model = %q, want the one qwen is part of\n%s", s.model, out.String())
	}
}
