package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A server on this machine that has no model by the name asked for is asked
// which it has, and the answer names them, since they are what is needed next.
func TestAnUnknownModelOnALocalServerNamesTheOnesItHas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/models"):
			w.Write([]byte(`{"data":[{"id":"llama3.2"},{"id":"qwen2.5-coder:7b"}]}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"message":"model \"qwen\" not found, try pulling it first"}}`))
		}
	}))
	defer srv.Close()

	var out strings.Builder
	err := Run(context.Background(), Options{
		Agent: "local", Wire: "openai", BaseURL: srv.URL + "/v1", Model: "qwen",
		Session: "s", Dir: t.TempDir(), In: strings.NewReader("hello\n/exit\n"), Out: &out, Width: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "it has llama3.2, qwen2.5-coder:7b -- /model <part of a name> switches") {
		t.Errorf("the models the server has were not named:\n%s", out.String())
	}
}
