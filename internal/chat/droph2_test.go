package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A stream an HTTP/2 server resets part-way is a dropped connection, as a TCP
// reset is: HTTP/2 says so in a frame of its own rather than by closing the
// socket, and Go reports it as a "stream error" that is neither.
func TestAStreamResetOverHTTP2IsADroppedConnection(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("the request came over HTTP/%d, want HTTP/2", r.ProtoMajor)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(": warm\n\n"))
		w.(http.Flusher).Flush()
		// A handler that gives up part-way has its stream reset, which is
		// what a proxy or a load balancer does to one it drops.
		panic(http.ErrAbortHandler)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	saved := httpClient
	t.Cleanup(func() { httpClient = saved })
	httpClient = srv.Client()
	httpClient.CheckRedirect = keepToTheAddress

	wire, err := NewWire("openai", srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	err = wire.Stream(context.Background(), Request{Model: "m"}, func(Event) {})
	if err == nil || !dropped(err) {
		t.Errorf("a reset stream is not a dropped connection: %v", err)
	}
}

// A server going away over HTTP/2 before the answer came is asked again, as
// any connection dropped that early is.
func TestAGoawayBeforeTheAnswerIsAskedAgain(t *testing.T) {
	saved := busyBackoff
	t.Cleanup(func() { busyBackoff = saved })
	busyBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	goaway := errors.New(`http2: server sent GOAWAY and closed the connection; LastStreamID=1, ErrCode=NO_ERROR, debug=""`)

	early := func(context.Context, Request, func(Event)) error { return goaway }
	wire := &scriptedWire{turns: []turnFunc{early, says("answered after all")}}
	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5"}, "hello\n/exit\n", wire)
	if !strings.Contains(out, "(the connection dropped before any of the answer came; trying again in") ||
		!strings.Contains(out, "answered after all") || len(wire.requests()) != 2 {
		t.Errorf("a GOAWAY was not asked again (%d requests):\n%s", len(wire.requests()), out)
	}

	// The same words in a refusal the API sent are the API's, not the
	// connection's.
	if dropped(&apiError{Code: 400, Status: "400 Bad Request", Msg: "stream error: no such field"}) {
		t.Errorf("an API's refusal was read as a dropped connection")
	}
}
