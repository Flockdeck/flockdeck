package chat

import (
	"strings"
	"testing"
)

// Started by hand with only a wire named, the chat looks for a stored key
// under the built-in agent that speaks that wire -- the id the error tells
// the user to store one under -- rather than under no id, where it was never
// found.
func TestAChatStartedWithOnlyAWireFindsItsStoredKey(t *testing.T) {
	for _, name := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "FLOCKDECK_API_KEY"} {
		t.Setenv(name, "")
	}
	stored := map[string]string{"google": "sk-google"}
	KeyStore = func(agent string) string { return stored[agent] }
	defer func() { KeyStore = nil }()

	if got, err := resolveKey(Options{Wire: "gemini"}); err != nil || got != "sk-google" {
		t.Errorf("resolveKey = %q, %v; want the key stored for google", got, err)
	}
	delete(stored, "google")
	_, err := resolveKey(Options{Wire: "gemini"})
	if err == nil || !strings.Contains(err.Error(), "flockdeck keys set google") {
		t.Errorf("error = %v, want it to name the id a key would be found under", err)
	}
}
