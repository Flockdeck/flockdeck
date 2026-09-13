package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/agent"
	"github.com/jmwri/flockdeck/internal/chat"
	"github.com/jmwri/flockdeck/internal/creds"
)

// TestEveryPlaceFindsTheKeyAPaneSends: the keys dialog, `flockdeck keys` and
// the pane's environment go by creds.Resolve, and a pane sends what the chat's
// own lookup finds. Both follow the one order -- the agent's own variables,
// never its vendor's where it talks to somebody else; then its stored key;
// then FLOCKDECK_API_KEY -- whichever of those hold a key, for a built-in at
// its vendor, a built-in pointed at a gateway, and an agent of the user's own
// with a variable of its own.
func TestEveryPlaceFindsTheKeyAPaneSends(t *testing.T) {
	saved := chat.KeyStore
	t.Cleanup(func() { chat.KeyStore = saved })
	chat.KeyStore = storedKey

	const (
		inVar    = "sk-test-in-its-variable"
		inStore  = "sk-test-stored"
		inShared = "sk-test-in-flockdeck-api-key"
	)
	said := map[string]string{"": "no key", inVar: "its variable", inStore: "its stored key", inShared: "FLOCKDECK_API_KEY"}
	for _, tc := range []struct {
		name string
		spec agent.Spec
		// counts says whether the agent's variable is one it may be given.
		counts bool
	}{
		{"a built-in at its vendor", agent.Spec{ID: "anthropic", Runner: agent.RunnerAPI,
			API: agent.APISpec{Wire: "anthropic", KeyEnv: []string{"ANTHROPIC_API_KEY"}}}, true},
		{"a built-in at a gateway", agent.Spec{ID: "anthropic", Runner: agent.RunnerAPI,
			API: agent.APISpec{Wire: "anthropic", BaseURL: "https://gw.example/v1", KeyEnv: []string{"ANTHROPIC_API_KEY"}}}, false},
		{"an agent with a variable of its own", agent.Spec{ID: "mine", Runner: agent.RunnerAPI,
			API: agent.APISpec{Wire: "anthropic", BaseURL: "https://gw.example/v1", KeyEnv: []string{"FLOCKDECK_TEST_GW_KEY"}}}, true},
	} {
		for held := 0; held < 8; held++ {
			inVarSet, stored, shared := held&1 != 0, held&2 != 0, held&4 != 0
			t.Run(fmt.Sprintf("%s, variable %v, stored %v, shared %v", tc.name, inVarSet, stored, shared), func(t *testing.T) {
				isolateKeys(t)
				t.Setenv("FLOCKDECK_TEST_GW_KEY", "")
				t.Setenv("FLOCKDECK_API_KEY", "")
				if inVarSet {
					// The vendor's variable is set beside the agent's own, since
					// it is the one an agent at a gateway must not be sent.
					t.Setenv("ANTHROPIC_API_KEY", inVar)
					for _, name := range tc.spec.API.KeyEnv {
						t.Setenv(name, inVar)
					}
				}
				if stored {
					if err := creds.Set(tc.spec.ID, inStore); err != nil {
						t.Fatal(err)
					}
				}
				if shared {
					t.Setenv("FLOCKDECK_API_KEY", inShared)
				}

				want := ""
				switch {
				case inVarSet && tc.counts:
					want = inVar
				case stored:
					want = inStore
				case shared:
					want = inShared
				}
				if got := creds.Resolve(tc.spec).Secret(); got != want {
					t.Errorf("creds.Resolve found %s, want %s", said[got], said[want])
				}
				// The pane: its environment gains what creds.Env hands it, and
				// its chat is started with the names chatArgs writes.
				for _, kv := range creds.Env(tc.spec) {
					name, value, _ := strings.Cut(kv, "=")
					t.Setenv(name, value)
				}
				got, _ := chat.KeyFor(chat.Options{Agent: tc.spec.ID, Wire: tc.spec.API.Wire,
					BaseURL: tc.spec.API.BaseURL, KeyEnv: agent.OwnKeyEnv(tc.spec.API)})
				if got != want {
					t.Errorf("a pane sends %s, want %s", said[got], said[want])
				}
			})
		}
	}
}
