package chat

import "testing"

// A host name has no case. The picker offers an endpoint at "LocalHost" as one
// that needs no key, so the chat must start there without one too.
func TestAnEndpointOnLocalhostInCapitalsNeedsNoKey(t *testing.T) {
	for _, name := range []string{"FLOCKDECK_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(name, "")
	}
	old := KeyStore
	KeyStore = nil
	t.Cleanup(func() { KeyStore = old })

	key, err := resolveKey(Options{Agent: "local", Wire: "openai", BaseURL: "http://LocalHost:11434/v1"})
	if err != nil || key != "" {
		t.Errorf("resolveKey = %q, %v; want no key, and no need of one", key, err)
	}
}
