package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// streamAnthropicErr answers one request with the given data lines, and returns
// the events the wire emitted and the error it ended with.
func streamAnthropicErr(t *testing.T, lines ...string) ([]Event, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			w.Write([]byte("data: " + l + "\n\n"))
		}
	}))
	defer srv.Close()
	var events []Event
	w := &anthropicWire{base: srv.URL}
	err := w.Stream(context.Background(), Request{Model: "m", MaxTokens: 50}, func(ev Event) {
		events = append(events, ev)
	})
	return events, err
}

func kinds(events []Event, k EventKind) int {
	n := 0
	for _, ev := range events {
		if ev.Kind == k {
			n++
		}
	}
	return n
}

// A body that ends before the answer does must not read as a finished answer,
// and above all must not hand a tool the first half of its arguments.
func TestAnAnswerCutOffMidStreamIsNotTakenAsFinished(t *testing.T) {
	events, err := streamAnthropicErr(t,
		`{"type":"message_start","message":{"usage":{"input_tokens":30}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"write_file"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\",\"content\":\"pack"}}`,
	)
	if err == nil || !strings.Contains(err.Error(), "before the answer was finished") {
		t.Fatalf("error = %v, want one saying the answer was cut off", err)
	}
	if kinds(events, EventCall) != 0 {
		t.Error("a call with half its arguments was handed on to be run")
	}
	if kinds(events, EventUsage) != 1 {
		t.Error("what the cut-off answer spent was not reported")
	}
}

func TestTheReasonAnAnswerStoppedEarlyIsSaid(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{
		{"max_tokens", "limit of 50 tokens"},
		{"refusal", "declined"},
	} {
		events, err := streamAnthropicErr(t,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partly"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"`+tc.reason+`"},"usage":{"output_tokens":50}}`,
			`{"type":"message_stop"}`,
		)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want one mentioning %q", tc.reason, err, tc.want)
		}
		if kinds(events, EventUsage) != 1 {
			t.Errorf("%s: what the answer spent was not reported", tc.reason)
		}
	}
}
