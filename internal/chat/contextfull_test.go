package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A conversation longer than the model can read is refused every time it is
// sent, so the way on is to start it over -- and the failure says so, rather
// than suggesting the retry that cannot work.
func TestAFullContextSaysToStartOver(t *testing.T) {
	for _, msg := range []string{
		"400 Bad Request: prompt is too long: 210000 tokens > 200000 maximum",
		"400 Bad Request: This model's maximum context length is 128000 tokens.",
		"400 Bad Request: The input token count (1100000) exceeds the maximum number of tokens allowed (1048576).",
	} {
		wire := &scriptedWire{turns: []turnFunc{
			func(context.Context, Request, func(Event)) error { return errors.New(msg) },
		}}
		out := run(t, Options{Agent: "anthropic", Task: "go on"}, "", wire)
		if !strings.Contains(out, "/clear starts it over") || strings.Contains(out, "/retry asks again") {
			t.Errorf("%s:\n%s", msg, out)
		}
	}
}

func TestAnthropicSaysWhenTheContextWindowIsFull(t *testing.T) {
	_, err := streamAnthropicErr(t,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partly"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"model_context_window_exceeded"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	)
	if !contextFull(err) {
		t.Errorf("error = %v, want one saying the context window is full", err)
	}
}
