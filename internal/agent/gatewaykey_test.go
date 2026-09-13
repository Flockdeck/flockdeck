package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestABuiltInPointedAtAGatewayIsNotGivenItsVendorsVariable: `flockdeck keys
// endpoint` and the picker's address field change a built-in's address and
// leave its entry's OPENAI_API_KEY where it was, so the pane was told to read
// the user's key for OpenAI and send it to the gateway. At the vendor's own
// address the variable is still the one it reads.
func TestABuiltInPointedAtAGatewayIsNotGivenItsVendorsVariable(t *testing.T) {
	gatewayed := Merge(&File{Agents: []json.RawMessage{
		json.RawMessage(`{"id": "openai", "api": {"baseURL": "https://gw.example/v1"}}`),
		json.RawMessage(`{"id": "google", "api": {"baseURL": "https://gw.example/gemini"}}`),
		json.RawMessage(`{"id": "mine", "api": {"wire": "openai", "baseURL": "https://gw.example/v1", "keyEnv": ["GW_KEY", "OPENAI_API_KEY"]}}`),
	}})
	for _, tc := range []struct {
		id     string
		vendor []string
		kept   []string
	}{
		{"openai", []string{"OPENAI_API_KEY"}, nil},
		{"google", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, nil},
		{"mine", []string{"OPENAI_API_KEY"}, []string{"GW_KEY"}},
	} {
		s, ok := gatewayed.Find(tc.id)
		if !ok {
			t.Fatalf("%s: not in the catalog", tc.id)
		}
		first, last := KeyNames(s)
		names := append(first, last...)
		argv := BuildArgv(s, false, Tokens{Session: "s", Model: "m", Prompt: "hi"})
		for _, v := range tc.vendor {
			if slices.Contains(names, v) {
				t.Errorf("%s at a gateway looks for its key in %s: %q", tc.id, v, names)
			}
			for _, a := range argv {
				if slices.Contains(strings.Split(a, ","), v) {
					t.Errorf("%s at a gateway is told to read %s: %q", tc.id, v, argv)
				}
			}
		}
		for _, v := range tc.kept {
			if !slices.Contains(names, v) || !slices.Contains(argv, v) {
				t.Errorf("%s lost a variable of its own, %s: names %q, argv %q", tc.id, v, names, argv)
			}
		}
	}

	vendor := Merge(&File{})
	for id, v := range map[string]string{"openai": "OPENAI_API_KEY", "anthropic": "ANTHROPIC_API_KEY"} {
		s, _ := vendor.Find(id)
		if first, _ := KeyNames(s); !slices.Contains(first, v) {
			t.Errorf("%s at its vendor's own address no longer reads %s: %q", id, v, first)
		}
	}
}
