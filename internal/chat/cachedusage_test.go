package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// streamUsage serves one streamed answer and returns the usage the wire
// reported for it.
func streamUsage(t *testing.T, w func(base string) Wire, body string) Usage {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Write([]byte(body))
	}))
	defer srv.Close()
	var usage Usage
	err := w(srv.URL).Stream(context.Background(), Request{Model: "m"}, func(ev Event) {
		if ev.Kind == EventUsage {
			usage = ev.Usage
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return usage
}

// OpenAI caches a long prompt unasked and bills the cached part at a tenth of
// the price; a conversation's history is nearly all of it. Counted as fresh
// input, a long chat's cost was shown several times over.
func TestOpenAICountsTheCachedPartOfThePrompt(t *testing.T) {
	u := streamUsage(t, func(base string) Wire { return &openaiWire{base: base} },
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: {\"usage\":{\"prompt_tokens\":10000,\"completion_tokens\":5,\"prompt_tokens_details\":{\"cached_tokens\":9000}}}\n\n"+
			"data: [DONE]\n\n")
	if u.In != 10000 || u.CacheRead != 9000 {
		t.Errorf("usage = %+v, want 10000 in of which 9000 read from the cache", u)
	}
}
