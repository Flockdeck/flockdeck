package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Gemini's own model list names models "models/gemini-...", and a name copied
// from it should reach the model rather than a 404.
func TestAGeminiModelNamedAsItsListNamesItIsFound(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]}` + "\n\n"))
	}))
	defer srv.Close()
	err := (&geminiWire{base: srv.URL}).Stream(context.Background(), Request{Model: "models/gemini-2.5-pro"}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/v1beta/models/gemini-2.5-pro:streamGenerateContent"; path != want {
		t.Errorf("asked %q, want %q", path, want)
	}
}
