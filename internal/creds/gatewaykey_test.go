package creds

import (
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
)

// A built-in pointed at a gateway keeps OPENAI_API_KEY in its entry. The key
// exported there is the user's for OpenAI: it is not the gateway's key, it is
// not said to be, and a key stored for the agent is not handed to its pane
// under that name either, where the chat would read it as the vendor's.
func TestAGatewayedBuiltInUsesItsStoredKeyNotTheVendors(t *testing.T) {
	isolateConfig(t)
	t.Setenv("FLOCKDECK_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-test-vendor")
	gw := agent.Spec{ID: "openai", Runner: agent.RunnerAPI, API: agent.APISpec{
		Wire: "openai", BaseURL: "https://gw.example/v1", KeyEnv: []string{"OPENAI_API_KEY"}}}

	if k := Resolve(gw); k.Set() {
		t.Errorf("with nothing stored the gateway's key is %v, the one exported for OpenAI", k)
	}
	if st := StatusOf(gw); st.Set || strings.Contains(st.Describe(), "OPENAI_API_KEY") {
		t.Errorf("status %+v (%q) offers OPENAI_API_KEY for a gateway", st, st.Describe())
	}

	if err := Set("openai", "sk-test-stored"); err != nil {
		t.Fatal(err)
	}
	if k := Resolve(gw); k.Secret() != "sk-test-stored" || k.Source != SourceStore {
		t.Errorf("Resolve = %v, want the key stored for the agent", k)
	}
	if st := StatusOf(gw); st.Source != SourceStore {
		t.Errorf("status %+v, want the stored key", st)
	}
	for _, kv := range Env(gw) {
		if strings.HasPrefix(kv, "OPENAI_API_KEY=") {
			t.Error("the stored key goes to the pane as OPENAI_API_KEY")
		}
	}

	// At OpenAI's own address the variable is the key, as it always was.
	vendor := gw
	vendor.API.BaseURL = ""
	if k := Resolve(vendor); k.Env != "OPENAI_API_KEY" {
		t.Errorf("at the vendor's address Resolve = %v, want OPENAI_API_KEY", k)
	}
}
