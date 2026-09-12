package chat

import "testing"

// An answer cut off at its limit keeps the reasoning the API finished before
// it -- signed, and passed back unchanged -- so that "go on" carries on from
// it; a block the stream never finished is not passed back.
func TestAnAnswerCutOffKeepsItsFinishedReasoning(t *testing.T) {
	body := "" +
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5,"output_tokens":1}}}` + "\n\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"working it out"}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-finished"}}` + "\n\n" +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"the first half"}}` + "\n\n" +
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"thinking","thinking":"","signature":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":2,"delta":{"type":"thinking_delta","thinking":"never signed"}}` + "\n\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":9}}` + "\n\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	events, err := streamFrom(t, "anthropic", body)
	if !cutOff(err) {
		t.Fatalf("err = %v, want an answer cut off at its limit", err)
	}
	var kept []Thinking
	for _, ev := range events {
		if ev.Kind == EventReasoning {
			kept = append(kept, ev.Thinking)
		}
	}
	if len(kept) != 1 || kept[0].Signature != "sig-finished" || kept[0].Text != "working it out" {
		t.Errorf("reasoning passed back = %+v, want only the finished, signed block", kept)
	}
}
