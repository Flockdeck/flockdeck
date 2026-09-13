package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// An API that asks to be left for ten minutes -- in seconds, or as the date a
// proxy writes instead -- is not asked again two and six seconds later, to be
// refused twice more. The pane says how long it was asked to wait and leaves
// the asking again to /retry.
func TestAnAPIThatAsksForALongWaitIsLeftToRetry(t *testing.T) {
	defer func(was []time.Duration) { busyBackoff = was }(busyBackoff)
	busyBackoff = []time.Duration{time.Millisecond, time.Millisecond}

	for _, c := range []struct{ name, header string }{
		{"in seconds", "600"},
		{"as a date", time.Now().Add(10 * time.Minute).UTC().Format(http.TimeFormat)},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls++
				n := calls
				mu.Unlock()
				if n == 1 {
					w.Header().Set("Retry-After", c.header)
					http.Error(w, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, http.StatusTooManyRequests)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"got there"}}` +
					"\n\n" + `data: {"type":"message_stop"}` + "\n\n"))
			}))
			defer srv.Close()

			var out strings.Builder
			t.Setenv("MY_TEST_KEY", "sk-test")
			err := Run(context.Background(), Options{
				Agent: "anthropic", Wire: "anthropic", BaseURL: srv.URL, KeyEnv: []string{"MY_TEST_KEY"},
				Model: "claude-sonnet-5", Session: "s", Dir: t.TempDir(), Task: "hello",
				In: strings.NewReader(""), Out: &out, Width: 200,
			})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if calls != 1 {
				t.Errorf("asked %d times, want once and then left to /retry:\n%s", calls, out.String())
			}
			if !strings.Contains(out.String(), "/retry asks again after that") {
				t.Errorf("the wait asked for was not said:\n%s", out.String())
			}
		})
	}
}
