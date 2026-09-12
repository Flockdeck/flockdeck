package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An agent whose catalog entry lists no models -- a local model server above
// all -- has /model ask the endpoint which it offers, so that one can be picked
// by number rather than named by an exact tag.
func TestModelAsksTheEndpointWhenTheCatalogHasNone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"data":[{"id":"qwen2.5-coder:7b"},{"id":"llama3.2"}]}`))
	}))
	defer srv.Close()

	var out strings.Builder
	err := Run(context.Background(), Options{
		Agent: "openai-compatible", Wire: "openai", BaseURL: srv.URL + "/v1",
		Session: "s", Dir: t.TempDir(), In: strings.NewReader("/model\n/model 2\n/status\n/exit\n"),
		Out: &out, Width: 70,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1  llama3.2", "2  qwen2.5-coder:7b", "answering with qwen2.5-coder:7b from here on"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestEveryWireCanListItsModels(t *testing.T) {
	for _, name := range []string{"anthropic", "openai", "gemini"} {
		w, err := NewWire(name, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := w.(modelLister); !ok {
			t.Errorf("the %s wire cannot list its models", name)
		}
	}
}
