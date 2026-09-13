package chat

import "testing"

// A key stored for the agent is sent after the agent's own variable and before
// FLOCKDECK_API_KEY, which any agent reads: the order the keys dialog and
// `flockdeck keys` report by (agent.KeyNames). A gateway with no variable of
// its own took FLOCKDECK_API_KEY first, while an agent with one was handed its
// stored key under that variable and so used it first.
func TestAStoredKeyComesBeforeFlockdecksOwnVariable(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "MY_TEST_KEY"} {
		t.Setenv(name, "")
	}
	t.Setenv("FLOCKDECK_API_KEY", "sk-test-shared")
	saved := KeyStore
	t.Cleanup(func() { KeyStore = saved })
	KeyStore = func(string) string { return "sk-test-stored" }

	gw := Options{Agent: "gw", Wire: "openai", BaseURL: "https://gw.example/v1"}
	own := Options{Agent: "anthropic", Wire: "anthropic", KeyEnv: []string{"MY_TEST_KEY"}}
	for _, o := range []Options{gw, own} {
		if key, from := lookupKey(o); key != "sk-test-stored" {
			t.Errorf("%s took its key %s, want the stored one", o.Agent, from)
		}
	}

	t.Setenv("MY_TEST_KEY", "sk-test-own")
	if key, from := lookupKey(own); key != "sk-test-own" {
		t.Errorf("an agent's own variable lost to its stored key: the key came %s", from)
	}

	KeyStore = func(string) string { return "" }
	if key, from := lookupKey(gw); key != "sk-test-shared" {
		t.Errorf("with nothing stored the gateway's key came %s, want FLOCKDECK_API_KEY", from)
	}
}
