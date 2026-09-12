package chat

import (
	"strings"
	"testing"
)

// A body that ends part-way -- a dropped connection, a proxy's timeout -- must
// not read as a finished answer on the OpenAI and Gemini wires either, and must
// not hand a tool the first half of its arguments.
func TestAnAnswerCutOffIsNotTakenAsFinishedOnEveryWire(t *testing.T) {
	for _, c := range []struct{ wire, body string }{
		{"openai", `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"write_file","arguments":"{\"path\":"}}]}}]}` + "\n\n"},
		{"gemini", `data: {"candidates":[{"content":{"parts":[{"text":"half an "}]}}]}` + "\n\n"},
	} {
		events, err := streamFrom(t, c.wire, c.body)
		if err == nil || !strings.Contains(err.Error(), "before the answer was finished") {
			t.Errorf("%s: error = %v, want one saying the answer was cut off", c.wire, err)
		}
		for _, ev := range events {
			if ev.Kind == EventCall {
				t.Errorf("%s: a call cut off mid-argument was handed on", c.wire)
			}
		}
	}
	// A server that sends the sentinel without a reason is heard out.
	if _, err := streamFrom(t, "openai", `data: {"choices":[{"delta":{"content":"whole"}}]}`+"\n\ndata: [DONE]\n\n"); err != nil {
		t.Errorf("an answer ended with [DONE] alone was refused: %v", err)
	}
}
