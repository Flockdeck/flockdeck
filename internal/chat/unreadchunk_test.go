package chat

import (
	"strings"
	"testing"
)

// A chunk that is not JSON, or an error in a shape the wire does not read, is
// what the server had to say instead of an answer, and is said rather than
// passed over.
func TestAChunkThatCannotBeReadIsNotPassedOverInSilence(t *testing.T) {
	for _, c := range []struct{ name, wire, body, want string }{
		{"openai plain text", "openai", "data: upstream request timeout\n\ndata: [DONE]\n\n", "upstream request timeout"},
		{"openai error of another shape", "openai", `data: {"error":{"message":{"detail":"the upstream model is gone"}}}` + "\n\ndata: [DONE]\n\n", "the upstream model is gone"},
		{"gemini plain text", "gemini", "data: upstream request timeout\n\n" +
			`data: {"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}` + "\n\n", "upstream request timeout"},
	} {
		_, err := streamFrom(t, c.wire, c.body)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error = %v, want one saying %q", c.name, err, c.want)
		}
	}

	// A chunk that is JSON in a shape this wire does not read -- content as a
	// list of parts, which some servers send -- does not end the answer.
	events, err := streamFrom(t, "openai", `data: {"choices":[{"delta":{"content":[{"type":"text","text":"x"}]}}]}`+"\n\n"+
		`data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	if err != nil {
		t.Errorf("a chunk of another shape ended the answer: %v", err)
	}
	if len(events) == 0 || events[0].Text != "done" {
		t.Errorf("events = %+v", events)
	}
}
