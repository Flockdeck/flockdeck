package chat

import (
	"strings"
	"testing"
)

// A chat talking to an address of its own -- a gateway, a proxy -- pays what
// that address charges, not the vendor's list price, and the picker already
// refuses to price such an agent. Its calls are counted in tokens and shown
// with no price, in the pane header and on the status line alike.
func TestCallsThroughAGatewayAreNotPricedFromTheTable(t *testing.T) {
	pane, reports := usageSink(t)
	wire := &scriptedWire{turns: []turnFunc{answersWith(Usage{In: 1_000_000, Out: 100_000})}}
	out := run(t, Options{
		Agent: "anthropic", Model: "claude-opus-5", Session: "pane-9", BaseURL: "https://gateway.example/v1",
		API: pane.srv.BaseURL(), Token: pane.srv.Token(),
	}, "hello\n", wire)

	got := reports()
	if len(got) != 1 || got[0].Tokens.In != 1_000_000 {
		t.Fatalf("got %+v, want the call's tokens", got)
	}
	if got[0].Cost.Known || got[0].Cost.USD != 0 {
		t.Errorf("cost = %+v, want none for a call through a gateway", got[0].Cost)
	}
	if strings.Contains(out, "~$") {
		t.Errorf("the status line priced a call through a gateway:\n%s", out)
	}
}
