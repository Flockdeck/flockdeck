package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A ceiling nobody asked for is refused by a model whose own limit is lower --
// an older OpenAI model, or a local model served with a short context -- so
// only the wire whose API insists on one sends one unasked.
func TestOnlyAnthropicSendsACeilingNobodyAskedFor(t *testing.T) {
	tests := []struct {
		wire, field string
		want        float64
	}{
		{"anthropic", "max_tokens", defaultMaxTokens},
		{"openai", "max_completion_tokens", 0},
		{"gemini", "generationConfig", 0},
	}
	for _, tc := range tests {
		t.Run(tc.wire, func(t *testing.T) {
			var sent map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&sent)
				w.Header().Set("Content-Type", "text/event-stream")
			}))
			defer srv.Close()
			wire, err := NewWire(tc.wire, srv.URL, "k")
			if err != nil {
				t.Fatal(err)
			}
			wire.Stream(context.Background(), Request{Model: "m"}, func(Event) {})

			got, present := sent[tc.field]
			if tc.want == 0 {
				if present {
					t.Errorf("%s was sent as %v with no ceiling asked for", tc.field, got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("%s = %v, want %v", tc.field, got, tc.want)
			}
		})
	}
}
