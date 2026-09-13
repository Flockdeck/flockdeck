package creds

import (
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// A key the chat would use -- the wire's usual variable, for an entry naming
// none of its own at the vendor's own address -- is shown as set, and where
// from, as the picker already counts it. A gateway given just an address is
// never sent the vendor's variable, so it is not shown as having that key:
// FLOCKDECK_API_KEY and then its stored key are what it uses, and a stored
// key a variable shadows is still reported stored. The OpenAI-compatible
// entry with no address is not offered on the strength of a key for OpenAI
// proper, and is not shown as having one.
func TestAKeyInTheWiresUsualVariableIsShownAsSet(t *testing.T) {
	isolateConfig(t)
	for _, name := range []string{"FLOCKDECK_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(name, "")
	}
	t.Setenv("OPENAI_API_KEY", "sk-test-wire")

	vendor := agent.Spec{ID: "oa", Runner: agent.RunnerAPI, API: agent.APISpec{Wire: "openai", BaseURL: "https://api.openai.com/v1"}}
	if st := StatusOf(vendor); !st.Set || st.Source != SourceEnv || st.Env != "OPENAI_API_KEY" {
		t.Errorf("status %+v, want set from OPENAI_API_KEY at OpenAI's own address", st)
	}

	gw := agent.Spec{ID: "gw", Runner: agent.RunnerAPI, API: agent.APISpec{Wire: "openai", BaseURL: "https://gw.example/v1"}}
	if st := StatusOf(gw); st.Set {
		t.Errorf("status %+v for a gateway with only OPENAI_API_KEY exported, want not set", st)
	}
	if err := Set("gw", "sk-test-stored"); err != nil {
		t.Fatal(err)
	}
	if st := StatusOf(gw); !st.Set || st.Source != SourceStore || !st.Stored {
		t.Errorf("status %+v, want the gateway's stored key, not OPENAI_API_KEY", st)
	}
	// Flockdeck's own variable comes before the store, for an entry the store
	// cannot be handed to by name, and the stored key can still be cleared.
	t.Setenv("FLOCKDECK_API_KEY", "sk-test-own")
	if st := StatusOf(gw); st.Source != SourceEnv || st.Env != "FLOCKDECK_API_KEY" || !st.Stored {
		t.Errorf("status %+v, want FLOCKDECK_API_KEY in use and the stored key still reported", st)
	}
	t.Setenv("FLOCKDECK_API_KEY", "")

	bare := agent.Spec{ID: agent.OpenAICompatibleID, Runner: agent.RunnerAPI, API: agent.APISpec{Wire: "openai"}}
	if st := StatusOf(bare); st.Set {
		t.Errorf("status %+v for an endpoint with no address, want not set", st)
	}

	// An entry with a variable of its own is handed its stored key under it,
	// ahead of the wire's usual one.
	own := apiSpec("mine", "FLOCKDECK_TEST_OWN_KEY")
	t.Setenv("FLOCKDECK_TEST_OWN_KEY", "")
	if err := Set("mine", "sk-test-mine"); err != nil {
		t.Fatal(err)
	}
	if st := StatusOf(own); st.Source != SourceStore {
		t.Errorf("status %+v, want the stored key", st)
	}
}
