package chat

import (
	"testing"
	"time"

	spending "github.com/jmwri/flockdeck/internal/spend"
)

// A thinking model's reasoning is billed as output and counted within it, and
// both OpenAI and Gemini say how much of the output it was. The pane header's
// tooltip names that part -- "50 out, 40 of them reasoning" -- so the chat
// reports it with each call.
func TestAThinkingModelsReasoningReachesThePaneHeader(t *testing.T) {
	u := streamUsage(t, func(base string) Wire { return &openaiWire{base: base} },
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: {\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"completion_tokens_details\":{\"reasoning_tokens\":40}}}\n\n"+
			"data: [DONE]\n\n")
	if u.Out != 50 || u.Reasoning != 40 {
		t.Errorf("OpenAI usage = %+v, want 50 out of which 40 reasoning", u)
	}
	u = streamUsage(t, func(base string) Wire { return &geminiWire{base: base} },
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]},\"finishReason\":\"STOP\"}],"+
			"\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":10,\"thoughtsTokenCount\":40}}\n\n")
	if u.Out != 50 || u.Reasoning != 40 {
		t.Errorf("Gemini usage = %+v, want 50 out of which 40 reasoning", u)
	}

	var got spending.Report
	r := &reporter{usageEndpoint: "http://127.0.0.1:1/usage", session: "p1",
		report: func(_, _ string, rep spending.Report, _ time.Duration) error { got = rep; return nil }}
	r.usage("gemini-3.8-flash", u)
	if got.Tokens.Out != 50 || got.Tokens.Reasoning != 40 {
		t.Errorf("reported %+v, want 50 out of which 40 reasoning", got.Tokens)
	}
}
