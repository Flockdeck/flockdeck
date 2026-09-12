package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// An overloaded API is the one failure asking again fixes, and a user should
// not have to type the prompt again to ask.
func TestABusyAPIIsAskedAgain(t *testing.T) {
	defer func(was []time.Duration) { busyBackoff = was }(busyBackoff)
	busyBackoff = []time.Duration{time.Millisecond, time.Millisecond}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, 529)
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
		In: strings.NewReader(""), Out: &out, Width: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "trying again") || !strings.Contains(out.String(), "got there") {
		t.Errorf("the busy API was not asked again:\n%s", out.String())
	}
	if calls != 2 {
		t.Errorf("asked %d times, want 2", calls)
	}
}

func TestABadRequestIsNotAskedAgain(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":{"message":"prompt is too long"}}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	t.Setenv("MY_TEST_KEY", "sk-test")
	var out strings.Builder
	Run(context.Background(), Options{
		Agent: "anthropic", Wire: "anthropic", BaseURL: srv.URL, KeyEnv: []string{"MY_TEST_KEY"},
		Model: "claude-sonnet-5", Session: "s", Dir: t.TempDir(), Task: "hello",
		In: strings.NewReader(""), Out: &out, Width: 60,
	})
	if calls != 1 {
		t.Errorf("a request the API refused was sent %d times", calls)
	}
}
