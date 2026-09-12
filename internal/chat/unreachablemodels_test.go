package chat

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// /model against a model server that is not running says so, as a failed
// answer does, rather than in the words of a failed dial.
func TestModelListSaysAServerThatIsNotRunningIsNotRunning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	var out strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = Run(ctx, Options{
		Agent: "local", Wire: "openai", BaseURL: "http://" + addr + "/v1",
		Session: "s", Dir: t.TempDir(), In: strings.NewReader("/model\n/exit\n"), Out: &out, Width: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "to say which models it has; is the model server running?") {
		t.Errorf("/model did not say the server is not answering:\n%s", out.String())
	}
	if strings.Contains(out.String(), "connectex") || strings.Contains(out.String(), "connection refused") {
		t.Errorf("/model gave the dial error's own words:\n%s", out.String())
	}
}
