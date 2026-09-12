package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An endpoint that does not know stream_options refuses the whole request over
// it, and would refuse every one. The request is made again without it.
func TestAnEndpointThatRefusesStreamOptionsIsAskedWithout(t *testing.T) {
	var asked []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		_, has := body["stream_options"]
		asked = append(asked, has)
		if has {
			http.Error(w, `{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	var text strings.Builder
	err := (&openaiWire{base: srv.URL}).Stream(context.Background(), Request{Model: "m"}, func(ev Event) {
		if ev.Kind == EventText {
			text.WriteString(ev.Text)
		}
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text.String() != "hello" {
		t.Errorf("answer = %q", text.String())
	}
	if len(asked) != 2 || !asked[0] || asked[1] {
		t.Errorf("requests carried stream_options %v, want once with and then once without", asked)
	}
}
