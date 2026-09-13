package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// An endpoint that redirects to another address is not followed there: the
// key travels in a header of the wire's own, which Go copies to wherever a
// redirect points, and the address that redirects is not the one the key was
// given for. A redirect on the same address is still followed.
func TestARedirectToAnotherAddressIsNotFollowedWithTheKey(t *testing.T) {
	var mu sync.Mutex
	elsewhereAsked, elsewhereKey := false, ""
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		elsewhereAsked, elsewhereKey = true, r.Header.Get("x-api-key")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"type":"message_stop"}` + "\n\n"))
	}))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()

	wire, err := NewWire("anthropic", redirecting.URL, "sk-test-redirect")
	if err != nil {
		t.Fatal(err)
	}
	err = wire.Stream(context.Background(), Request{Model: "m"}, func(Event) {})
	mu.Lock()
	asked, key := elsewhereAsked, elsewhereKey
	mu.Unlock()
	if key != "" {
		t.Error("the key was sent to the address the endpoint redirected to")
	}
	if asked || err == nil {
		t.Errorf("the redirect to another address was followed (error %v)", err)
	}

	// The same address moving a path is an ordinary redirect, and followed.
	moving := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages" {
			http.Redirect(w, r, "/v2/messages", http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"moved"}}` +
			"\n\n" + `data: {"type":"message_stop"}` + "\n\n"))
	}))
	defer moving.Close()
	wire, err = NewWire("anthropic", moving.URL, "sk-test-redirect")
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	err = wire.Stream(context.Background(), Request{Model: "m"}, func(ev Event) {
		if ev.Kind == EventText {
			text.WriteString(ev.Text)
		}
	})
	if err != nil || text.String() != "moved" {
		t.Errorf("a redirect on the same address was not followed: %q, %v", text.String(), err)
	}
}
