package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// An error the OpenAI or Gemini wire sends part-way through a stream is kept
// with the status it stands for, as the Anthropic wire keeps one, so that a
// busy API is known for one whether it said so before the stream or in it.
func TestAnErrorInTheStreamKeepsItsStatusOnEveryWire(t *testing.T) {
	for _, c := range []struct {
		name, wire, body string
		code             int
	}{
		{"openai server error", "openai", `{"error":{"message":"The server had an error while processing your request.","type":"server_error","code":null}}`, 500},
		{"openai rate limit", "openai", `{"error":{"message":"Rate limit reached for requests","type":"requests","code":"rate_limit_exceeded"}}`, 429},
		{"a gateway's numeric code", "openai", `{"error":{"code":502,"message":"Provider returned error"}}`, 502},
		{"gemini overloaded", "gemini", `{"error":{"code":503,"message":"The model is overloaded. Please try again later.","status":"UNAVAILABLE"}}`, 503},
		{"gemini internal", "gemini", `{"error":{"code":500,"message":"An internal error has occurred.","status":"INTERNAL"}}`, 500},
	} {
		_, err := streamFrom(t, c.wire, "data: "+c.body+"\n\n")
		e, ok := busy(err)
		if !ok || e.Code != c.code {
			t.Errorf("%s: error %v is not a busy API's %d", c.name, err, c.code)
		}
	}

	// One that is not a busy API is still an error, and still said in the
	// vendor's words.
	_, err := streamFrom(t, "openai", `data: {"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`+"\n\n")
	if _, ok := busy(err); ok || err == nil || !strings.Contains(err.Error(), "Incorrect API key provided") {
		t.Errorf("a refusal in the stream = %v", err)
	}
}

// An API that fails in the stream before any of the answer came is asked
// again, as one that failed with a status is.
func TestAnErrorInTheStreamBeforeTheAnswerIsAskedAgain(t *testing.T) {
	saved := busyBackoff
	t.Cleanup(func() { busyBackoff = saved })
	busyBackoff = []time.Duration{time.Millisecond, time.Millisecond}

	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if asked.Add(1) == 1 {
			w.Write([]byte(`data: {"error":{"message":"The server had an error while processing your request.","type":"server_error","code":null}}` + "\n\n"))
			return
		}
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"answered after all"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	wire, err := NewWire("openai", srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	out := run(t, Options{Agent: "openai", Model: "m"}, "hello\n/exit\n", wire)
	if !strings.Contains(out, "trying again in") || !strings.Contains(out, "answered after all") || asked.Load() != 2 {
		t.Errorf("an error in the stream was not asked again (%d requests):\n%s", asked.Load(), out)
	}
}
