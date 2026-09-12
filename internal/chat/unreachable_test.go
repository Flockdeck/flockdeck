package chat

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// A model server on this machine that is not running is said to be not
// running, rather than the answer being said to have failed and /retry being
// offered as though asking again would help.
func TestAnEndpointNotAnsweringIsSaidToBeUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens there now

	var out strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = Run(ctx, Options{
		Agent: "local", Wire: "openai", BaseURL: "http://" + addr + "/v1", Model: "qwen",
		Session: "s", Dir: t.TempDir(), In: strings.NewReader("hello\n/exit\n"), Out: &out, Width: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"could not reach the endpoint:", "is the model server running?", "flockdeck keys endpoint local <url>"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "the model could not answer") {
		t.Errorf("an unreachable server was reported as a failed answer:\n%s", out.String())
	}
	if !unreachable(&net.DNSError{Err: "no such host", Name: "no-such-host.example"}) {
		t.Error("a host that does not exist is not taken as unreachable")
	}
}
