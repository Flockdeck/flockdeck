package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// runAgainst drives one turn against an Anthropic endpoint whose answers are
// given in order, and returns what was drawn and how many requests were made.
func runAgainst(t *testing.T, bodies ...string) (string, int) {
	t.Helper()
	defer func(was []time.Duration) { busyBackoff = was }(busyBackoff)
	busyBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := bodies[min(calls, len(bodies)-1)]
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(body))
	}))
	defer srv.Close()
	t.Setenv("MY_TEST_KEY", "sk-test")
	var out strings.Builder
	Run(context.Background(), Options{
		Agent: "anthropic", Wire: "anthropic", BaseURL: srv.URL, KeyEnv: []string{"MY_TEST_KEY"},
		Model: "claude-sonnet-5", Session: "s", Dir: t.TempDir(), Task: "hello",
		In: strings.NewReader(""), Out: &out, Width: 60,
	})
	return out.String(), calls
}

const (
	overloaded = `data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}` + "\n\n"
	answered   = `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"got there"}}` + "\n\n" +
		`data: {"type":"message_stop"}` + "\n\n"
)

// The API can say it is overloaded in the stream as well as in the status,
// usually before any of the answer; that is asked again like the status is.
func TestAnOverloadedStreamIsAskedAgain(t *testing.T) {
	out, calls := runAgainst(t, overloaded, answered)
	if calls != 2 || !strings.Contains(out, "got there") {
		t.Errorf("made %d requests and drew:\n%s", calls, out)
	}
}

// Once some of the answer has been drawn, asking again would draw it twice.
func TestAnAnswerCutOffPartWayIsNotAskedAgain(t *testing.T) {
	partial := `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"half an "}}` + "\n\n" + overloaded
	out, calls := runAgainst(t, partial, answered)
	if calls != 1 {
		t.Errorf("asked %d times after part of the answer had been drawn", calls)
	}
	if !strings.Contains(out, "could not answer") {
		t.Errorf("the failure was not said:\n%s", out)
	}
}
