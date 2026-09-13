package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The servers that speak the OpenAI wire put its root wherever they like, and
// a base with a path of its own is that root: Cloudflare's gateway, Gemini's
// OpenAI endpoint and Zhipu's were each sent to a /v1 they do not have. A base
// with a query -- Azure's api-version -- keeps it, after the path rather than
// in front of it. The other two wires still add their version where a base
// lacks it.
func TestEndpointsAreBuiltForTheShapesServersGiveTheirRoots(t *testing.T) {
	var mu sync.Mutex
	var path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		path, query = r.URL.Path, r.URL.RawQuery
		mu.Unlock()
		http.Error(w, `{"error":{"message":"not here"}}`, http.StatusNotFound)
	}))
	defer srv.Close()

	for _, c := range []struct {
		name, wire, base, wantPath, wantQuery string
		list                                  bool
	}{
		{name: "Cloudflare's gateway", wire: "openai", base: "/v1/acct/gw/openai",
			wantPath: "/v1/acct/gw/openai/chat/completions"},
		{name: "Gemini's OpenAI endpoint", wire: "openai", base: "/v1beta/openai/",
			wantPath: "/v1beta/openai/chat/completions"},
		{name: "Zhipu", wire: "openai", base: "/api/paas/v4",
			wantPath: "/api/paas/v4/chat/completions"},
		{name: "Zhipu's models", wire: "openai", base: "/api/paas/v4", list: true,
			wantPath: "/api/paas/v4/models"},
		{name: "Azure", wire: "openai", base: "/openai/deployments/d?api-version=2024-10-21",
			wantPath: "/openai/deployments/d/chat/completions", wantQuery: "api-version=2024-10-21"},
		{name: "a bare root", wire: "openai", base: "",
			wantPath: "/v1/chat/completions"},
		{name: "an Anthropic gateway without the version", wire: "anthropic", base: "/anthropic",
			wantPath: "/anthropic/v1/messages"},
		{name: "a Gemini proxy with a query of its own", wire: "gemini", base: "/proxy?tenant=a",
			wantPath: "/proxy/v1beta/models/m1:streamGenerateContent", wantQuery: "tenant=a&alt=sse"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, err := NewWire(c.wire, srv.URL+c.base, "k")
			if err != nil {
				t.Fatal(err)
			}
			if c.list {
				w.(modelLister).ListModels(context.Background())
			} else {
				w.Stream(context.Background(), Request{Model: "m1"}, func(Event) {})
			}
			mu.Lock()
			defer mu.Unlock()
			if path != c.wantPath || query != c.wantQuery {
				t.Errorf("asked %q with query %q, want %q with %q", path, query, c.wantPath, c.wantQuery)
			}
		})
	}
}
