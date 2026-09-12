package chat

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// A connection that breaks while the answer is arriving is said to have
// dropped, not reported in the socket's own words.
func TestADroppedConnectionIsSaidInWords(t *testing.T) {
	reset := func(_ context.Context, _ Request, emit func(Event)) error {
		emit(Event{Kind: EventText, Text: "half an"})
		return &net.OpError{Op: "read", Net: "tcp", Err: errors.New("wsarecv: An existing connection was forcibly closed by the remote host.")}
	}
	wire := &scriptedWire{turns: []turnFunc{reset, says("the whole answer")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "hello\n/retry\n/exit\n", wire)

	if !strings.Contains(out, "the connection to the endpoint dropped part-way through the answer") ||
		strings.Contains(out, "wsarecv") {
		t.Errorf("the drop was not said in words:\n%s", out)
	}
	if !strings.Contains(out, "the whole answer") {
		t.Errorf("/retry did not ask again:\n%s", out)
	}
}
