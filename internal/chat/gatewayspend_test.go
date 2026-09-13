package chat

import "testing"

// A chat pointed at an address of the user's own -- a gateway, a server of
// their own -- is not priced even for a model that has a price here, and its
// report says that is why. Without the reason, the pane header told the user
// that a model it knows the price of had none.
func TestCallsThroughAGatewaySayWhyTheyAreNotPriced(t *testing.T) {
	pane, reports := usageSink(t)
	wire := &scriptedWire{turns: []turnFunc{answersWith(Usage{In: 300, Out: 40})}}
	run(t, Options{
		Agent: "anthropic", Wire: "anthropic", Model: "claude-opus-5", BaseURL: "https://gateway.example",
		Session: "pane-9", API: pane.srv.BaseURL(), Token: pane.srv.Token(),
	}, "hello\n", wire)

	got := reports()
	if len(got) != 1 || got[0].Tokens.In != 300 || got[0].Cost.Known || got[0].Cost.Source != "gateway" {
		t.Errorf("got %+v, want 300 tokens in, unpriced because of the gateway", got)
	}
}
