package agent

import (
	"slices"
	"testing"
)

// TestABuiltInPointedAtAGatewayIsNotGivenItsVendorsVariable: an entry
// pointed at a gateway kept its vendor's OPENAI_API_KEY, so the user's key for
// OpenAI was sent to the gateway. At the vendor's own
// address the variable is still the one it reads.
func TestABuiltInPointedAtAGatewayIsNotGivenItsVendorsVariable(t *testing.T) {
	for _, tc := range []struct {
		spec   Spec
		vendor []string
		kept   []string
	}{
		{Spec{ID: "openai", API: APISpec{Wire: "openai", BaseURL: "https://gw.example/v1", KeyEnv: []string{"OPENAI_API_KEY"}}}, []string{"OPENAI_API_KEY"}, nil},
		{Spec{ID: "google", API: APISpec{Wire: "gemini", BaseURL: "https://gw.example/gemini", KeyEnv: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}}}, []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, nil},
		{Spec{ID: "mine", API: APISpec{Wire: "openai", BaseURL: "https://gw.example/v1", KeyEnv: []string{"GW_KEY", "OPENAI_API_KEY"}}}, []string{"OPENAI_API_KEY"}, []string{"GW_KEY"}},
	} {
		first, last := KeyNames(tc.spec)
		names := append(first, last...)
		for _, v := range tc.vendor {
			if slices.Contains(names, v) {
				t.Errorf("%s at a gateway looks for its key in %s: %q", tc.spec.ID, v, names)
			}
		}
		for _, v := range tc.kept {
			if !slices.Contains(names, v) {
				t.Errorf("%s lost a variable of its own, %s: %q", tc.spec.ID, v, names)
			}
		}
	}
	for _, spec := range []Spec{
		{ID: "openai", API: APISpec{Wire: "openai", KeyEnv: []string{"OPENAI_API_KEY"}}},
		{ID: "anthropic", API: APISpec{Wire: "anthropic", KeyEnv: []string{"ANTHROPIC_API_KEY"}}},
	} {
		if first, _ := KeyNames(spec); !slices.Contains(first, spec.API.KeyEnv[0]) {
			t.Errorf("%s at its vendor's own address no longer reads %s: %q", spec.ID, spec.API.KeyEnv[0], first)
		}
	}
}
