package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A stream that breaks part-way -- an error sent in it, Ctrl+C, a connection
// dropped -- has already read the prompt, cache and all, and written some of
// the answer, and that is spent whether or not the answer is whole. Returned
// without it, the spend was never counted.
func TestAStreamThatBreaksStillSaysWhatItSpent(t *testing.T) {
	for _, c := range []struct {
		name string
		wire func(base string) Wire
		body string
		want Usage
	}{
		{
			name: "anthropic",
			wire: func(base string) Wire { return &anthropicWire{base: base} },
			// The cached part is counted within the input, as it is billed.
			want: Usage{In: 16, CacheRead: 5},
			body: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11,\"cache_read_input_tokens\":5}}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"half\"}}\n\n" +
				"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n",
		},
		{
			name: "gemini",
			wire: func(base string) Wire { return &geminiWire{base: base} },
			want: Usage{In: 11, Out: 2},
			body: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"half\"}]}}],\"usageMetadata\":{\"promptTokenCount\":11,\"candidatesTokenCount\":2}}\n\n" +
				"data: {\"error\":{\"message\":\"the backend went away\"}}\n\n",
		},
		{
			name: "openai",
			wire: func(base string) Wire { return &openaiWire{base: base} },
			want: Usage{In: 11, Out: 2},
			body: "data: {\"choices\":[{\"delta\":{\"content\":\"half\"}}]}\n\n" +
				"data: {\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":2}}\n\n" +
				"data: {\"error\":{\"message\":\"the backend went away\"}}\n\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte(c.body))
			}))
			defer srv.Close()
			var usage Usage
			err := c.wire(srv.URL).Stream(context.Background(), Request{Model: "m"}, func(ev Event) {
				if ev.Kind == EventUsage {
					usage = ev.Usage
				}
			})
			if err == nil {
				t.Fatal("a stream that broke part-way was read as finished")
			}
			if usage != c.want {
				t.Errorf("usage = %+v, want %+v, what was read before it broke", usage, c.want)
			}
		})
	}
}
