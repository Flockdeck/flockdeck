package chat

import (
	"strings"
	"testing"
)

// A chat started by hand with a wire and a gateway's address, and no agent,
// took the key stored for the built-in that speaks the wire -- the user's key
// for OpenAI -- and sent it to the gateway. It has no agent whose stored key
// is the gateway's, so it looks for none, and says to name one. Started at the
// vendor's own address, the built-in's stored key is still found.
func TestAChatStartedByHandAtAGatewayIsNotSentTheVendorsStoredKey(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "FLOCKDECK_API_KEY"} {
		t.Setenv(name, "")
	}
	saved := KeyStore
	t.Cleanup(func() { KeyStore = saved })
	KeyStore = func(agent string) string {
		if agent == "openai" {
			return "sk-test-stored-for-openai"
		}
		return ""
	}

	byHand := Options{Wire: "openai", BaseURL: "https://gw.example/v1"}
	if key, from := lookupKey(byHand); key != "" {
		t.Errorf("a chat started by hand at a gateway found a key %s", from)
	}
	_, err := resolveKey(byHand)
	if err == nil || strings.Contains(err.Error(), "keys set openai") || !strings.Contains(err.Error(), "-agent") {
		t.Errorf("error = %v, want one saying to name an agent rather than to store a key for OpenAI", err)
	}

	if key, from := lookupKey(Options{Wire: "openai"}); key != "sk-test-stored-for-openai" {
		t.Errorf("at OpenAI's own address the key came %s, want the one stored for openai", from)
	}
}
