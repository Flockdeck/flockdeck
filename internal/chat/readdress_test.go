package chat

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// An address that cannot be reached, and has been changed since the chat
// started, is given up for the new one, as the pane's own advice promises.
func TestAChangedAddressIsTakenUpWhenTheOldOneCannotBeReached(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gone := "http://" + ln.Addr().String() + "/v1"
	ln.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"answered at the new address"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	moved := srv.URL + "/v1"

	saved := EndpointStore
	t.Cleanup(func() { EndpointStore = saved })
	EndpointStore = func(agent string) string {
		if agent != "local" {
			t.Errorf("looked up %q", agent)
		}
		return moved
	}

	var out strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = Run(ctx, Options{
		Agent: "local", Wire: "openai", BaseURL: gone, Model: "qwen",
		Session: "s", Dir: t.TempDir(), In: strings.NewReader("hello\n/exit\n"), Out: &out, Width: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"(the address is now " + moved + "; asking there)", "answered at the new address"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "could not reach the endpoint") {
		t.Errorf("the old address's failure was reported although the new one answered:\n%s", out.String())
	}
}
