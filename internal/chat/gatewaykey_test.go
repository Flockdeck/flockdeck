package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// An agent pointed at a gateway, with no key variable of its own, is sent the
// key stored for it -- never OPENAI_API_KEY, the user's key for OpenAI, which
// the chat used to find first and hand to whoever runs the gateway. At
// OpenAI's own address the usual variable is still the key.
func TestAGatewayIsNotSentTheVendorsKey(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "FLOCKDECK_API_KEY"} {
		t.Setenv(name, "")
	}
	t.Setenv("OPENAI_API_KEY", "sk-test-vendor")
	saved := KeyStore
	t.Cleanup(func() { KeyStore = saved })
	KeyStore = func(agent string) string {
		if agent == "gw" {
			return "sk-test-stored-for-gw"
		}
		return ""
	}

	var mu sync.Mutex
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	var out strings.Builder
	err := Run(context.Background(), Options{
		Agent: "gw", Wire: "openai", BaseURL: srv.URL + "/v1", Model: "m",
		Session: "s", Dir: t.TempDir(), Task: "hi", In: strings.NewReader(""), Out: &out, Width: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	switch sent {
	case "Bearer sk-test-stored-for-gw":
	case "Bearer sk-test-vendor":
		t.Error("the gateway was sent OPENAI_API_KEY, the key for OpenAI, over the key stored for it")
	default:
		t.Errorf("the gateway was sent neither key (%d characters)", len(sent))
	}

	// With nothing stored for it the gateway has no key, rather than the
	// vendor's, and is told where one goes.
	KeyStore = func(string) string { return "" }
	gw := Options{Agent: "gw", Wire: "openai", BaseURL: "https://gw.example/v1"}
	if key, from := lookupKey(gw); key != "" {
		t.Errorf("a gateway with nothing stored found a key %s", from)
	}
	if _, err := resolveKey(gw); err == nil || strings.Contains(err.Error(), "OPENAI_API_KEY") || !strings.Contains(err.Error(), "FLOCKDECK_API_KEY") {
		t.Errorf("error = %v, want one naming FLOCKDECK_API_KEY and not OPENAI_API_KEY", err)
	}

	// A built-in pointed at a gateway is started still naming its vendor's
	// variable, and is not sent it either.
	KeyStore = func(agent string) string {
		if agent == "openai" {
			return "sk-test-stored-for-openai"
		}
		return ""
	}
	builtin := Options{Agent: "openai", Wire: "openai", BaseURL: "https://gw.example/v1", KeyEnv: []string{"OPENAI_API_KEY"}}
	if key, from := lookupKey(builtin); key != "sk-test-stored-for-openai" {
		t.Errorf("a built-in at a gateway took its key %s, want the one stored for it", from)
	}
	KeyStore = func(string) string { return "" }

	for _, base := range []string{"", "https://api.openai.com/v1"} {
		if key, from := lookupKey(Options{Agent: "openai", Wire: "openai", BaseURL: base}); key != "sk-test-vendor" {
			t.Errorf("at OpenAI's own address %q the key came %s, want from OPENAI_API_KEY", base, from)
		}
	}
}
